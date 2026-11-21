"""Importações do titular. Uma leitura incompleta não substitui uma posição confirmada."""
import csv
import hashlib
import io
import os
import re
import time
from datetime import date, datetime, timedelta, timezone
from decimal import Decimal, InvalidOperation
from threading import Lock
from uuid import UUID

import requests
from flask import Blueprint, jsonify, request
from sqlalchemy import JSON, DateTime, String, func, select, text
from sqlalchemy.orm import Mapped, mapped_column

from .db import Base, db_session
from .finance import Entry, Position, upsert_entry, upsert_position

bp = Blueprint("banking", __name__)
BASE_URL = "https://api.pluggy.ai"
PRODUCTS = ["ACCOUNTS", "CREDIT_CARDS", "TRANSACTIONS", "INVESTMENTS"]
COLUMNS = {
    "positions": ["external_id", "name", "identifier", "asset_class", "value", "institution",
                  "currency", "issuer", "indexer", "maturity", "separate"],
    "entries": ["external_id", "description", "booked_on", "amount", "kind", "category",
                "merchant", "installment_group", "installment_index", "account_id", "bill_id", "separate"],
}
_auth_lock = Lock()
_key = ""
_key_until = 0


class Connection(Base):
    __tablename__ = "oc_connections"
    id: Mapped[str] = mapped_column(String(36), primary_key=True)
    name: Mapped[str] = mapped_column(String(180), default="")
    status: Mapped[str] = mapped_column(String(100), default="Aguardando sincronização")
    updated_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    attempted_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    snapshot: Mapped[dict | None] = mapped_column(JSON)


class ImportAlias(Base):
    __tablename__ = "oc_import_aliases"
    kind: Mapped[str] = mapped_column(String(12), primary_key=True)
    external_id: Mapped[str] = mapped_column(String(255), primary_key=True)
    record_id: Mapped[int]


def _lock_import(db):
    if db.bind.dialect.name == "postgresql":
        db.execute(text("SELECT pg_advisory_xact_lock(73148611)"))


def _import_row(db, kind, data, source):
    """O extrato pode usar o ID cru do banco, um ID próprio ou apenas os dados da operação."""
    model, save = (Position, upsert_position) if kind == "positions" else (Entry, upsert_entry)
    key = str(data["external_id"])
    alias = db.get(ImportAlias, (kind, key))
    found = db.get(model, alias.record_id) if alias else None
    bank_key = key if key.startswith("pluggy:") else "pluggy:" + key
    if found is None:
        keys = [key, bank_key, key.removeprefix("pluggy:")]
        found = db.scalar(select(model).where(model.external_id.in_(keys)).order_by(model.id))
    if found is None and str(data.get("separate", "")).lower() not in {"1", "true", "sim"}:
        opposite = model.source.in_(("csv", "manual")) if source == "pluggy" else model.source == "pluggy"
        query = select(model).where(opposite, model.active.is_(True))
        if kind == "positions":
            ref = data.get("identifier") or ""
            label = model.identifier == ref if ref else func.lower(model.name) == str(data.get("name", "")).lower()
            query = query.where(label, model.currency == (data.get("currency") or "BRL").upper())
            if data.get("institution"):
                query = query.where(func.lower(model.institution).in_(("", data["institution"].lower())))
            if data.get("maturity"):
                query = query.where(model.maturity == _date(data["maturity"]))
        else:
            day = _date(data.get("booked_on") or data.get("date"))
            query = query.where(model.booked_on == day, model.amount == money(data.get("amount")),
                                func.lower(model.description) == str(data.get("description", "")).lower())
            if data.get("account_id"):
                query = query.where(model.account_id.in_(("", data["account_id"])))
            if data.get("installment_index"):
                query = query.where(model.installment_index == int(data["installment_index"]))
        matches = list(db.scalars(query.limit(3)))
        if len(matches) > 1:
            ids = ", ".join(x.external_id for x in matches)
            raise ValueError(f"Mais de um registro corresponde a esta linha. Use o external_id correto ({ids}) "
                             "ou marque separate=sim se for outro registro.")
        if matches:
            found = matches[0]
    if found is None:
        return save(db, data, source)
    if source == "csv" and found.source == "pluggy":
        db.merge(ImportAlias(kind=kind, external_id=key, record_id=found.id))
        return found
    if source == "pluggy" and found.external_id != bank_key:
        db.merge(ImportAlias(kind=kind, external_id=found.external_id, record_id=found.id))
        found.external_id = bank_key
        found.source = source
        db.flush()
    return save(db, {**data, "external_id": found.external_id}, source)


def configured():
    return bool(os.getenv("PLUGGY_CLIENT_ID") and os.getenv("PLUGGY_CLIENT_SECRET"))


def _api(method, path, **kwargs):
    global _key, _key_until
    with _auth_lock:
        if time.monotonic() >= _key_until:
            response = requests.post(BASE_URL + "/auth", timeout=(5, 20), json={
                "clientId": os.environ["PLUGGY_CLIENT_ID"], "clientSecret": os.environ["PLUGGY_CLIENT_SECRET"]})
            response.raise_for_status()
            _key = response.json()["apiKey"]
            _key_until = time.monotonic() + 6500
        key = _key
    result = requests.request(method, BASE_URL + path, headers={"X-API-KEY": key},
                              timeout=(5, 35), allow_redirects=False, **kwargs)
    if result.status_code == 401:
        with _auth_lock:
            _key_until = 0
    result.raise_for_status()
    return result.json()


def _records(path, params, *, cursor=False):
    page, suffix, seen = 1, "", set()
    while True:
        query = (None if suffix else params) if cursor else {**params, "page": page}
        payload = _api("GET", path + suffix, params=query)
        if not isinstance(payload.get("results"), list):
            raise ValueError("Resposta incompleta da instituição.")
        yield from payload["results"]
        if not cursor:
            if page >= int(payload.get("totalPages", 1)):
                return
            page += 1
            continue
        suffix = payload.get("next")
        if not suffix:
            return
        if not isinstance(suffix, str) or not suffix.startswith("?") or suffix in seen:
            raise ValueError("Paginação inválida da instituição.")
        seen.add(suffix)


def money(value):
    amount = Decimal(str(value))
    if not amount.is_finite():
        raise ValueError("Valor monetário inválido.")
    return amount


def _date(value):
    return date.fromisoformat(str(value)[:10])


def bank_snapshot(db):
    accounts, bills, timestamps = {}, {}, []
    complete = fresh = forecast = True
    now = datetime.now(timezone.utc)
    for item in db.scalars(select(Connection).order_by(Connection.updated_at)):
        if item.snapshot is None:
            complete = fresh = forecast = False
            continue
        stamp = item.snapshot.get("source_updated_at")
        last = datetime.fromisoformat(stamp) if stamp else item.updated_at
        current = (last is not None and now - last.replace(tzinfo=last.tzinfo or timezone.utc) < timedelta(hours=36)
                   and item.status == "Atualizada")
        fresh = fresh and current
        forecast = forecast and current and item.snapshot.get("forecast_complete", False)
        for account in item.snapshot["accounts"]:
            accounts[account["id"]] = account
        for bill in item.snapshot["bills"]:
            bills[bill["id"]] = {**bill, "due": _date(bill["due"]), "amount": money(bill["amount"])}
        if item.updated_at:
            timestamps.append(item.updated_at)
    balances = [a["balance"] for a in accounts.values() if a["type"] == "BANK" and a["currency"] == "BRL"]
    return {"balance": sum(map(money, balances)) if balances and complete and None not in balances else None,
            "bills": list(bills.values()),
            "updated_at": min(timestamps) if timestamps else None,
            "balance_fresh": bool(timestamps) and fresh,
            "forecast_complete": bool(timestamps) and forecast}


def _owner_id():
    return os.getenv("PLUGGY_OWNER_ID", "openchris-personal")


def allowed_ids():
    return {str(UUID(value.strip())) for value in os.getenv("PLUGGY_ITEM_IDS", "").split(",") if value.strip()}


def _position(raw, institution, updated_at):
    subtype = raw.get("subtype") or ""
    classes = {"REAL_ESTATE_FUND": "Fundos imobiliários", "ETF": "ETFs", "STOCK": "Ações",
               "FIXED_INCOME": "Renda fixa", "MUTUAL_FUND": "Fundos", "TREASURY": "Renda fixa",
               "RETIREMENT": "Previdência"}
    name = raw.get("name") or raw.get("code") or "Título sem nome"
    active = raw.get("status") in ("ACTIVE", None)
    return {"external_id": "pluggy:" + str(raw["id"]), "identifier": raw.get("isin") or raw.get("code") or "",
            "name": name, "asset_class": classes.get(subtype) or classes.get(raw.get("type"), ""),
            "issuer": raw.get("issuer") or "", "indexer": raw.get("rateType") or "",
            "maturity": str(raw["dueDate"])[:10] if raw.get("dueDate") else None,
            "institution": institution, "value": raw.get("balance") if active else (raw.get("balance") or 0),
            "currency": raw["currencyCode"], "updated_at": raw.get("updatedAt") or updated_at, "active": active}


def _party_document(party):
    document = party.get("documentNumber") if isinstance(party, dict) else None
    if not isinstance(document, dict):
        return None
    number = re.sub(r"[.\s/-]", "", str(document.get("value") or ""))
    if len(number) in (11, 14) and number.isascii() and number.isdecimal() and len(set(number)) > 1:
        return number
    return None


def _entry(raw, account, bills):
    amount = money(raw["amount"])
    normalized_amount = raw.get("amountInAccountCurrency")
    if account["currency"] != "BRL" or (raw.get("currencyCode") not in ("BRL", None) and normalized_amount is None):
        return None
    if normalized_amount is not None:
        amount = money(normalized_amount)
    card = account["type"] == "CREDIT"
    outflow = (card and amount > 0) or (not card and amount < 0)
    op = raw.get("operationType")
    provider_category = str(raw.get("category") or "")
    cat_key = provider_category.strip().casefold()
    description = raw.get("description") or "Lançamento"
    bank_bill = (not card and outflow and cat_key in ("transfer", "transfers")
                 and " ".join(description.casefold().split()) in ("pagamento de fatura", "pagamento fatura"))
    payment = raw.get("paymentData") or {}
    payer = _party_document(payment.get("payer"))
    receiver = _party_document(payment.get("receiver"))
    own_transfer = (cat_key == "same person transfer" or cat_key.startswith("same person transfer - ")
                    or cat_key in ("transfer between my accounts", "transferências entre contas próprias"))
    if payer and receiver:
        rails = ("PIX", "TED", "DOC")
        own_transfer = payer == receiver and (own_transfer or op in rails or payment.get("paymentMethod") in rails)
    if bank_bill or op == "PAGAMENTO_FATURA" or cat_key in ("credit card payment", "pagamento de fatura", "pagamento de cartão de crédito"):
        kind = "bill_payment"
    elif op in ("ESTORNO", "CASHBACK"):
        kind = "refund"
    elif (not card and own_transfer) or cat_key in ("investment", "investments") or op == "RESGATE_APLIC_FINANCEIRA":
        kind = "transfer"
    elif card and amount < 0:
        kind = "unknown"
    elif op == "FOLHA_PAGAMENTO":
        kind = "income"
    else:
        kind = "expense" if outflow else "unknown"
    detail = raw.get("creditCardMetadata") or {}
    bill_id = detail.get("billId")
    booked = bills[bill_id]["due"] if bill_id in bills else str(raw["date"])[:10]
    return {"external_id": "pluggy:" + str(raw["id"]), "description": description,
            "merchant": (raw.get("merchant") or {}).get("name") or "", "amount": abs(amount),
            "booked_on": booked, "kind": kind, "category": provider_category if kind == "expense" else "",
            "card": card, "account_id": account["id"], "bill_id": bill_id or "",
            "bank_direction": "outflow" if outflow else "inflow",
            "scheduled": raw.get("status") == "PENDING" and not card,
            "installment_index": detail.get("installmentNumber"),
            "installment_group": detail.get("purchaseId") or None}


def _read_item(item_id):
    meta = _api("GET", "/items/" + item_id)
    if meta.get("id") != item_id:
        raise ValueError("Conexão divergente.")
    if meta.get("clientUserId") != _owner_id() and item_id not in allowed_ids():
        raise ValueError("Autorize este identificador em PLUGGY_ITEM_IDS para usar uma conexão anterior.")
    return meta


def sync_item(item_id):
    meta = _read_item(item_id)
    if meta.get("status") not in {"UPDATED", "PARTIAL_SUCCESS"}:
        with db_session() as db:
            db.get(Connection, item_id).status = "Instituição: " + str(meta.get("status", "indisponível"))[:55]
        return
    if meta["status"] == "PARTIAL_SUCCESS":
        with db_session() as db:
            db.get(Connection, item_id).status = "Leitura parcial na instituição. Última posição preservada."
        return
    reported = set(meta["products"]) if isinstance(meta.get("products"), list) else set(PRODUCTS)
    connector = meta.get("connector") or {}
    reads_positions = "INVESTMENTS" in reported or (connector.get("name") == "MeuPluggy"
                      and "INVESTMENTS" in (connector.get("products") or []))
    accounts, bills, entries = [], {}, []
    account_rows = _records("/accounts", {"itemId": item_id}) if reported.intersection(PRODUCTS[:3]) else ()
    for account in account_rows:
        aid = account["id"]
        data = {"id": aid, "type": account["type"], "currency": account.get("currencyCode"),
                "balance": str(account["balance"]) if account.get("balance") is not None else None}
        accounts.append(data)
        if data["type"] == "CREDIT" and "CREDIT_CARDS" in reported:
            for bill in _records("/bills", {"accountId": aid}):
                if bill.get("totalAmountCurrencyCode") != "BRL":
                    continue
                paid = sum(money(p["amount"]) for p in bill.get("payments", []) if p.get("currencyCode") == "BRL")
                bills[bill["id"]] = {"id": bill["id"], "due": _date(bill["dueDate"]).isoformat(),
                                      "account_id": aid, "amount": str(max(0, money(bill["totalAmount"]) - paid))}
        txs = _records("/v2/transactions", {"accountId": aid}, cursor=True) if "TRANSACTIONS" in reported else ()
        for raw in txs:
            row = _entry(raw, data, bills)
            if row and row["amount"] > 0:
                entries.append(row)
    institution = (meta.get("connector") or {}).get("name") or "Instituição conectada"
    positions = [_position(raw, institution, meta.get("lastUpdatedAt"))
                 for raw in _records("/investments", {"itemId": item_id})] if reads_positions else []
    with db_session() as db:
        _lock_import(db)
        conn = db.get(Connection, item_id)
        old_snapshot = conn.snapshot or {}
        other_positions = set()
        other_entries = set()
        for other in db.scalars(select(Connection).where(Connection.id != item_id)):
            other_positions.update((other.snapshot or {}).get("positions", []))
            other_entries.update((other.snapshot or {}).get("entries", []))
        for row in entries:
            _import_row(db, "entries", row, "pluggy")
        active_ids = set()
        for row in positions:
            active_ids.add(row["external_id"])
            if row["value"] is None:
                raise ValueError("A instituição enviou um investimento sem saldo.")
            _import_row(db, "positions", row, "pluggy")
        old_ids = set(old_snapshot.get("positions", []))
        old_entries = set(old_snapshot.get("entries", []))
        entry_ids = {row["external_id"] for row in entries}
        if (old_ids and reads_positions and not positions) or (old_entries and "TRANSACTIONS" in reported and not entries):
            raise ValueError("O provedor retornou uma lista vazia após uma leitura com dados.")
        if reads_positions:
            for old in db.scalars(select(Position).where(Position.external_id.in_(old_ids - active_ids - other_positions))):
                old.active = False
        if entries:
            earliest_by_account = {}
            for row in entries:
                account = row["account_id"]
                day = _date(row["booked_on"])
                earliest_by_account[account] = min(day, earliest_by_account.get(account, day))
            missing = old_entries - entry_ids - other_entries
            for old in db.scalars(select(Entry).where(Entry.external_id.in_(missing))):
                if old.account_id in earliest_by_account and old.booked_on >= earliest_by_account[old.account_id]:
                    old.active = False
        conn.name = institution
        conn.snapshot = {"accounts": accounts, "bills": list(bills.values()),
                         "source_updated_at": meta.get("lastUpdatedAt"),
                         "forecast_complete": {"ACCOUNTS", "TRANSACTIONS", "CREDIT_CARDS"} <= set(meta.get("products") or []),
                         "positions": sorted(active_ids if reads_positions else old_ids),
                         "entries": sorted(entry_ids if "TRANSACTIONS" in reported else old_entries)}
        conn.updated_at = datetime.now(timezone.utc)
        conn.status = "Atualizada"


def sync_due():
    if not configured():
        return
    now = datetime.now(timezone.utc)
    with db_session() as db:
        for item_id in allowed_ids():
            if not db.get(Connection, item_id):
                db.add(Connection(id=item_id))
        db.flush()
        due = [item.id for item in db.scalars(select(Connection)) if not item.attempted_at or
               now - item.attempted_at.replace(tzinfo=timezone.utc) >= timedelta(hours=1)]
    for item_id in due:
        with db_session() as db:
            db.get(Connection, item_id).attempted_at = now
        try:
            sync_item(item_id)
        except (requests.RequestException, ValueError, KeyError, InvalidOperation) as exc:
            with db_session() as db:
                status = ("Conciliação pendente: há registros equivalentes no extrato."
                          if str(exc).startswith("Mais de um registro") else
                          "Falha ao consultar. Nova tentativa automática em uma hora.")
                db.get(Connection, item_id).status = status


@bp.get("/api/connections")
def connections():
    with db_session() as db:
        items = [{"id": i.id, "name": i.name, "status": i.status,
                  "updated_at": i.updated_at.isoformat() if i.updated_at else None} for i in db.scalars(select(Connection))]
    return jsonify(pluggy_configured=configured(), items=items, csv_columns=COLUMNS)


@bp.post("/api/connections/token")
def token():
    if not configured():
        return jsonify(error="Configure as credenciais da Pluggy no Compose."), 409
    try:
        options = {"clientUserId": _owner_id(), "avoidDuplicates": True, "products": PRODUCTS}
        if os.getenv("PUBLIC_BASE_URL"):
            options["oauthRedirectUri"] = os.environ["PUBLIC_BASE_URL"].rstrip("/") + "/mais"
        res = _api("POST", "/connect_token", json={"options": options})
        return jsonify(access_token=res["accessToken"])
    except (requests.RequestException, KeyError, ValueError):
        return jsonify(error="A Pluggy não emitiu um token. Verifique as credenciais e o domínio autorizado."), 502


@bp.post("/api/connections")
def connect():
    if not configured():
        return jsonify(error="Configure a Pluggy para conectar a instituição."), 409
    try:
        data = request.get_json(silent=True)
        if not isinstance(data, dict):
            raise ValueError
        item_id = str(UUID(str(data.get("item_id", ""))))
        meta = _read_item(item_id)
    except (ValueError, TypeError):
        return jsonify(error="Identificador inválido ou conexão de outro titular. Confira PLUGGY_ITEM_IDS para uma conexão anterior."), 400
    except requests.RequestException:
        return jsonify(error="Não foi possível consultar essa conexão na Pluggy."), 502
    with db_session() as db:
        item = db.get(Connection, item_id)
        if not item:
            db.add(Connection(id=item_id, name=(meta.get("connector") or {}).get("name", "")))
        else:
            item.attempted_at = None
    return jsonify(status="queued"), 202


@bp.post("/api/import")
def import_csv():
    kind = request.form.get("kind")
    upload = request.files.get("file")
    if kind not in COLUMNS or upload is None:
        return jsonify(error="Selecione o tipo e um extrato CSV."), 400
    line = 1
    try:
        content = upload.read().decode("utf-8-sig", errors="strict")
        dialect = csv.Sniffer().sniff(content[:4096], delimiters=",;\t")
        reader = csv.DictReader(io.StringIO(content, newline=""), dialect=dialect, strict=True)
        required = {"name", "value"} if kind == "positions" else {"description", "amount"}
        fields = set(reader.fieldnames or [])
        if not required <= fields or (kind == "entries" and not fields.intersection({"booked_on", "date"})):
            raise ValueError("Confira os nomes das colunas no modelo.")
        seen = {}
        count = 0
        with db_session() as db:
            _lock_import(db)
            for line, raw in enumerate(reader, 2):
                if not any(raw.values()):
                    continue
                if line > 10001 or None in raw:
                    raise ValueError("Limite de linhas ou colunas inválidas.")
                data = {k: v.strip() for k, v in raw.items() if v and v.strip()}
                for field in ("value", "amount"):
                    if "," in data.get(field, ""):
                        data[field] = data[field].replace(".", "").replace(",", ".")
                if not data.get("external_id"):
                    if kind == "positions":
                        identity = (data.get("institution", ""), data.get("identifier") or data.get("name"),
                                    data.get("maturity", ""), data.get("issuer", ""))
                    else:
                        identity = (data.get("account_id", ""), data.get("booked_on") or data.get("date"),
                                    data.get("description"), data.get("amount"), data.get("installment_index"))
                    digest = hashlib.sha256(repr(identity).encode()).hexdigest()
                    seen[digest] = seen.get(digest, 0) + 1
                    data["external_id"] = f"csv:{digest}:{seen[digest]}"
                _import_row(db, kind, data, "csv")
                count += 1
            if not count:
                raise ValueError("O arquivo não contém registros.")
    except (ValueError, ArithmeticError, csv.Error, KeyError, TypeError) as exc:
        if isinstance(exc, UnicodeError):
            detail = "Salve o arquivo em UTF-8."
        else:
            detail = str(exc) if isinstance(exc, ValueError) else "Confira os campos e o separador."
        return jsonify(error=f"Linha {line}. {detail}"), 400
    return jsonify(count=count, imported=count)
