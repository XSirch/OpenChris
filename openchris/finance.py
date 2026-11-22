"""Carteira, lançamentos e cálculos financeiros da conta pessoal."""
from __future__ import annotations

import calendar
import hashlib
import re
from collections import defaultdict
from datetime import date, datetime, timedelta, timezone
from decimal import Decimal, InvalidOperation, ROUND_FLOOR, ROUND_HALF_UP, localcontext
from uuid import uuid4
from zoneinfo import ZoneInfo

from sqlalchemy import JSON, Boolean, Date, DateTime, ForeignKey, Numeric, String, select
from sqlalchemy.orm import Mapped, mapped_column

from .db import Base

CENT = Decimal("0.01")
ZERO = Decimal("0.00")
HUNDRED = Decimal("100.00")
MAX_MONEY = Decimal("999999999999.99")
MONEY = Numeric(16, 2)
UNKNOWN_CLASS = "A classificar"
UNCATEGORIZED = "Sem categoria"
INVESTMENT_INCOME = "Rendimentos de investimentos"
INSTALLMENT_DISCOUNT = "Desconto de antecipação"
KINDS = {"expense", "income", "transfer", "refund", "bill_payment", "unknown"}
CLASSES = ("Renda fixa", "Ações", "Fundos imobiliários", "Exterior")


class Position(Base):
    __tablename__ = "positions"
    id: Mapped[int] = mapped_column(primary_key=True)
    external_id: Mapped[str] = mapped_column(String(255), unique=True)
    identifier: Mapped[str] = mapped_column(default="")
    name: Mapped[str]
    asset_class: Mapped[str] = mapped_column(default=UNKNOWN_CLASS)
    issuer: Mapped[str] = mapped_column(default="")
    indexer: Mapped[str] = mapped_column(default="")
    maturity: Mapped[date | None] = mapped_column(Date)
    institution: Mapped[str] = mapped_column(default="")
    value: Mapped[Decimal] = mapped_column(MONEY)
    currency: Mapped[str] = mapped_column(String(3), default="BRL")
    active: Mapped[bool] = mapped_column(default=True)
    source: Mapped[str]
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True))
    field_sources: Mapped[dict] = mapped_column(JSON, default=dict)
    conflicts: Mapped[dict] = mapped_column(JSON, default=dict)
    needs_review: Mapped[bool] = mapped_column(Boolean, default=False)


class Entry(Base):
    __tablename__ = "entries"
    id: Mapped[int] = mapped_column(primary_key=True)
    external_id: Mapped[str] = mapped_column(String(255), unique=True)
    original_description: Mapped[str]
    original_kind: Mapped[str]
    original_category: Mapped[str]
    description: Mapped[str]
    merchant: Mapped[str] = mapped_column(default="")
    amount: Mapped[Decimal] = mapped_column(MONEY)
    kind: Mapped[str] = mapped_column(default="unknown")
    bank_direction: Mapped[str | None] = mapped_column(String(7))
    category: Mapped[str] = mapped_column(default=UNCATEGORIZED)
    booked_on: Mapped[date] = mapped_column(Date, index=True)
    source: Mapped[str]
    classification_source: Mapped[str]
    installment_group: Mapped[str | None]
    installment_index: Mapped[int | None]
    advanced_by: Mapped[int | None] = mapped_column(ForeignKey("entries.id"))
    account_id: Mapped[str] = mapped_column(default="")
    bill_id: Mapped[str] = mapped_column(default="")
    card: Mapped[bool] = mapped_column(default=False)
    scheduled: Mapped[bool] = mapped_column(default=False)
    active: Mapped[bool] = mapped_column(default=True)


class CorrectionRule(Base):
    __tablename__ = "correction_rules"
    key: Mapped[str] = mapped_column(primary_key=True)
    kind: Mapped[str]
    category: Mapped[str]


class Profile(Base):
    __tablename__ = "profile"
    id: Mapped[int] = mapped_column(primary_key=True)
    goal: Mapped[str | None]
    horizon_months: Mapped[int | None]
    reserve: Mapped[Decimal | None] = mapped_column(MONEY)
    risk: Mapped[str | None]
    targets: Mapped[dict | None] = mapped_column(JSON)
    balance: Mapped[Decimal | None] = mapped_column(MONEY)
    commitments: Mapped[Decimal | None] = mapped_column(MONEY)
    minimum: Mapped[Decimal | None] = mapped_column(MONEY)
    next_income_date: Mapped[date | None] = mapped_column(Date)


def today_local():
    return datetime.now(ZoneInfo("America/Sao_Paulo")).date()


def money(value, label="valor", *, signed=False):
    try:
        result = Decimal(str(value))
        if not result.is_finite() or result.copy_abs() > MAX_MONEY or (not signed and result < 0):
            raise ValueError
        rounded = result.quantize(CENT, rounding=ROUND_HALF_UP)
        if result != rounded:
            raise ValueError
        return rounded
    except (InvalidOperation, ValueError):
        raise ValueError(f"Informe {label} em reais, com até duas casas decimais.") from None


def _text(value, label, *, limit=255, required=False):
    if value is None:
        value = ""
    if not isinstance(value, str):
        raise ValueError(f"Confira o campo {label}.")
    value = value.strip()
    if (required and not value) or len(value) > limit:
        raise ValueError(f"Confira o campo {label}.")
    return value


def _day(value, label="data"):
    if isinstance(value, datetime):
        return value.date()
    if isinstance(value, date):
        return value
    try:
        if not re.fullmatch(r"\d{4}-\d{2}-\d{2}", str(value)):
            raise ValueError
        return date.fromisoformat(value)
    except (TypeError, ValueError):
        raise ValueError(f"Informe {label} no formato AAAA-MM-DD.") from None


def _integer(value, label, lower=1, upper=1200):
    if not re.fullmatch(r"[0-9]{1,10}", str(value)) or not lower <= int(value) <= upper:
        raise ValueError(f"Informe {label} entre {lower} e {upper}.")
    return int(value)


def _row(session, model, data, source):
    external = data.get("external_id")
    if external is None:
        if source != "manual":
            raise ValueError("A importação precisa de um identificador estável.")
        external = f"manual:{uuid4()}"
    external = _text(external, "identificador externo", required=True)
    row = session.scalar(select(model).where(model.external_id == external).with_for_update()
                         .execution_options(populate_existing=True))
    if row is None:
        row = model(external_id=external, source=source)
        session.add(row)
    return row


_FACT_LABELS = {"identifier": "código", "issuer": "emissor", "indexer": "indexador",
                "asset_class": "classe", "institution": "instituição", "maturity": "vencimento"}


def _set_fact(position, key, value, source):
    setattr(position, key, value)
    position.field_sources = {**(position.field_sources or {}), key: source}


def upsert_position(session, data, source="manual"):
    position = _row(session, Position, data, source)
    fresh = position.id is None
    timestamp = data.get("updated_at")
    if timestamp:
        try:
            stamp = timestamp if isinstance(timestamp, datetime) else datetime.fromisoformat(timestamp)
        except (ValueError, TypeError):
            raise ValueError("Confira a data de atualização da posição.") from None
        stamp = stamp.astimezone(timezone.utc) if stamp.tzinfo else stamp.replace(tzinfo=timezone.utc)
        if not fresh and stamp < position.updated_at.replace(tzinfo=timezone.utc):
            return position
    else:
        stamp = datetime.now(timezone.utc)
    if fresh or "name" in data:
        _set_fact(position, "name", _text(data.get("name"), "nome", required=True), source)
    if fresh or "value" in data:
        _set_fact(position, "value", money(data.get("value")), source)
    if "currency" in data:
        currency = _text(data["currency"], "moeda").upper()
        if not re.fullmatch(r"[A-Z]{3}", currency):
            raise ValueError("Informe o código de três letras da moeda.")
        _set_fact(position, "currency", currency, source)
    if "active" in data:
        if not isinstance(data["active"], bool):
            raise ValueError("Confira a situação da posição.")
        position.active = data["active"]
    for key, label in _FACT_LABELS.items():
        value = data.get(key)
        if value in ("", None) or (key == "asset_class" and value == UNKNOWN_CLASS):
            continue
        value = _day(value, label) if key == "maturity" else _text(value, label)
        if source != "manual" and (position.field_sources or {}).get(key) == "manual":
            continue
        _set_fact(position, key, value, source)
        if source == "manual" and key in (position.conflicts or {}):
            position.conflicts = {k: v for k, v in position.conflicts.items() if k != key}
    position.needs_review = bool(position.conflicts) or not position.asset_class or position.asset_class == UNKNOWN_CLASS
    if data.get("needs_review") is True:
        position.needs_review = True
    position.updated_at = stamp
    position.source = source
    session.flush()
    return position


def apply_position_facts(session, position_id, facts, source):
    position = session.get(Position, position_id, with_for_update=True, populate_existing=True)
    if position is None or not position.active:
        return False
    for key, label in _FACT_LABELS.items():
        raw = facts.get(key)
        if raw in (None, "", UNKNOWN_CLASS):
            continue
        value = _day(raw, label) if key == "maturity" else _text(raw, label)
        current = getattr(position, key)
        if current in (None, "", UNKNOWN_CLASS):
            _set_fact(position, key, value, source)
        elif current != value:
            position.conflicts = {**(position.conflicts or {}), key: {"value": str(value), "source": source}}
    position.needs_review = bool(position.conflicts) or position.asset_class == UNKNOWN_CLASS
    return not position.conflicts


def _rule_key(entry):
    prefix = entry.original_kind + ":"
    if entry.bank_direction:
        prefix += entry.bank_direction + ":"
    if entry.merchant:
        return prefix + "merchant:" + " ".join(entry.merchant.casefold().split())
    return prefix + "description:" + " ".join(entry.original_description.casefold().split())


def _classification(kind, category):
    if not isinstance(kind, str) or kind not in KINDS:
        raise ValueError("Escolha um tipo válido para o lançamento.")
    return kind, _text(category, "categoria", limit=100) or UNCATEGORIZED


def _direction_allows(direction, kind):
    return not ((direction == "inflow" and kind == "expense")
                or (direction == "outflow" and kind in ("income", "refund")))


def apply_card_installment_discount(entry):
    if (entry.active and not entry.advanced_by and entry.source == "pluggy" and entry.card
            and entry.bank_direction == "inflow" and entry.kind == "unknown" and entry.classification_source == "pending"
            and " ".join(entry.original_description.casefold().split()) in (
                "desc antecipa parcelas", "desconto por antecipação de parcelas")):
        entry.kind, entry.category, entry.classification_source = "refund", INSTALLMENT_DISCOUNT, "source"


def upsert_entry(session, data, source="manual"):
    entry = _row(session, Entry, data, source)
    fresh = entry.id is None
    entry.active = True
    direction = data.get("bank_direction")
    if direction is not None:
        if direction not in ("inflow", "outflow"):
            raise ValueError("Confira a direção do lançamento no extrato.")
        if source != "pluggy" and entry.bank_direction not in (None, direction):
            raise ValueError("A direção do lançamento já foi registrada no extrato.")
        entry.bank_direction = direction
    if fresh or "description" in data:
        entry.description = _text(data.get("description"), "descrição", limit=1000, required=True)
    if fresh:
        entry.original_description = entry.description
    if fresh or "date" in data or "booked_on" in data:
        entry.booked_on = _day(data.get("booked_on", data.get("date")))
    if fresh or "amount" in data:
        entry.amount = money(data.get("amount"))
    for key in ("merchant", "account_id", "bill_id", "installment_group"):
        if key in data:
            value = _text(data[key], key)
            setattr(entry, key, value or (None if key == "installment_group" else ""))
    if "installment_index" in data:
        index = data["installment_index"]
        entry.installment_index = None if index in (None, "") else _integer(index, "a parcela")
    for key in ("card", "scheduled"):
        if key in data:
            if not isinstance(data[key], bool):
                raise ValueError("Confira a indicação de cartão ou agendamento.")
            setattr(entry, key, data[key])
    confirmed_bank_movement = False
    if fresh or "kind" in data or "category" in data:
        kind, category = _classification(data.get("kind", entry.kind or "unknown"),
                                         data.get("category", entry.category))
        confirmed_bank_movement = (source == "pluggy"
                                   and (kind in ("transfer", "bill_payment") or (kind, category) == ("income", INVESTMENT_INCOME))
                                   and entry.classification_source != "manual")
        if fresh or confirmed_bank_movement:
            entry.original_kind, entry.original_category = kind, category
        if confirmed_bank_movement or entry.classification_source not in ("manual", "codex", "rule") or source == "manual":
            entry.kind, entry.category = kind, category
            pending = kind == "unknown" or (kind in ("expense", "refund") and category == UNCATEGORIZED)
            entry.classification_source = "pending" if pending else ("manual" if source == "manual" else "source")
    if not _direction_allows(entry.bank_direction, entry.kind):
        if source == "manual":
            raise ValueError("O tipo escolhido contradiz a entrada ou saída registrada no extrato.")
        entry.kind, entry.category, entry.classification_source = "unknown", UNCATEGORIZED, "pending"
    if not confirmed_bank_movement and entry.classification_source != "manual":
        with session.no_autoflush:
            rule = session.get(CorrectionRule, _rule_key(entry))
        if rule and _direction_allows(entry.bank_direction, rule.kind):
            entry.kind, entry.category, entry.classification_source = rule.kind, rule.category, "rule"
    if source == "pluggy":
        apply_card_installment_discount(entry)
    session.flush()
    return entry


def classify_entry(session, entry_id, kind, category, source="codex"):
    entry = session.get(Entry, entry_id, with_for_update=True, populate_existing=True)
    if entry is None or not entry.active or (source == "codex" and entry.classification_source != "pending"):
        return False
    kind, category = _classification(kind, category)
    if not _direction_allows(entry.bank_direction, kind):
        if source == "manual":
            raise ValueError("O tipo escolhido contradiz a entrada ou saída registrada no extrato.")
        return False
    if source == "codex" and (kind == "unknown" or (kind in ("expense", "refund") and category == UNCATEGORIZED)):
        return False
    entry.kind, entry.category, entry.classification_source = kind, category, source
    return True


def correct_entry(session, data):
    entry_id = _integer(data.get("entry_id"), "o lançamento", upper=2147483647)
    if not classify_entry(session, entry_id, data.get("kind"), data.get("category"), source="manual"):
        raise ValueError("Lançamento não encontrado.")
    entry = session.get(Entry, entry_id)
    remember = data.get("remember", True)
    if not isinstance(remember, bool):
        raise ValueError("Confira se a regra deve ser lembrada.")
    if remember:
        key = _rule_key(entry)
        session.merge(CorrectionRule(key=key, kind=entry.kind, category=entry.category))
        candidates = select(Entry).where(Entry.active.is_(True), Entry.original_kind == entry.original_kind,
                                         Entry.classification_source != "manual")
        for other in session.scalars(candidates.with_for_update()):
            if _rule_key(other) == key and _direction_allows(other.bank_direction, entry.kind):
                other.kind, other.category, other.classification_source = entry.kind, entry.category, "rule"
    return entry


def advance_installments(session, data):
    ids = data.get("entry_ids")
    if not isinstance(ids, list) or not ids or len(ids) > 600:
        raise ValueError("Selecione as parcelas antecipadas.")
    ids = sorted({_integer(i, "a parcela", upper=2147483647) for i in ids})
    booked = _day(data.get("date"))
    if booked > today_local():
        raise ValueError("Informe a data em que a antecipação foi cobrada.")
    paid = money(data.get("amount"), "o total cobrado")
    if paid <= 0:
        raise ValueError("O total cobrado precisa ser positivo.")
    rows = list(session.scalars(select(Entry).where(Entry.id.in_(ids)).order_by(Entry.id)
                               .with_for_update().execution_options(populate_existing=True)))
    groups = {(row.account_id, row.installment_group) for row in rows}
    if (len(rows) != len(ids) or len(groups) != 1 or not rows[0].installment_group
            or any(row.kind != "expense" or row.booked_on < booked or (not row.active and not row.advanced_by)
                   for row in rows)):
        raise ValueError("Selecione parcelas da mesma compra com vencimento a partir da cobrança.")
    old_ids = {row.advanced_by for row in rows}
    old = None
    if old_ids != {None}:
        if len(old_ids) != 1 or None in old_ids:
            raise ValueError("Uma das parcelas já foi antecipada.")
        old = session.get(Entry, next(iter(old_ids)))
        if old.booked_on != booked or old.amount != paid:
            raise ValueError("Essas parcelas já têm outra antecipação.")
        previous_ids = set(session.scalars(select(Entry.id).where(Entry.advanced_by == old.id,
                                                                  Entry.installment_group.is_not(None))))
        if previous_ids != set(ids):
            raise ValueError("A cobrança já inclui um conjunto diferente de parcelas.")
    charge_id = data.get("charge_entry_id")
    if charge_id is not None:
        charge = session.get(Entry, _integer(charge_id, "a cobrança", upper=2147483647), with_for_update=True)
        if (charge is None or not charge.active or charge.id in ids or charge.booked_on != booked or charge.amount != paid
                or charge.advanced_by or charge.kind not in ("unknown", "expense") or not _direction_allows(charge.bank_direction, "expense")
                or (charge.account_id and rows[0].account_id and charge.account_id != rows[0].account_id)):
            raise ValueError("A cobrança precisa ter a mesma data, conta e valor da antecipação.")
        if old and old.id != charge.id and old.source != "advance":
            raise ValueError("As parcelas já estão vinculadas a outra cobrança da fatura.")
        other = session.scalar(select(Entry.id).where(Entry.advanced_by == charge.id, Entry.id.not_in(ids),
                                                     Entry.installment_group.is_not(None)))
        if other:
            raise ValueError("A cobrança já está vinculada a outras parcelas.")
    elif old:
        return old
    else:
        reference = hashlib.sha256(",".join(map(str, ids)).encode()).hexdigest()
        charge = upsert_entry(session, {
            "external_id": "advance:" + reference, "description": rows[0].description,
            "merchant": rows[0].merchant, "date": booked, "amount": paid, "kind": "expense",
            "category": rows[0].category, "account_id": rows[0].account_id, "card": rows[0].card,
        }, source="advance")
    charge.kind, charge.category, charge.classification_source = "expense", rows[0].category, "manual"
    if old and old.id != charge.id:
        old.advanced_by = charge.id
    for row in rows:
        row.advanced_by = charge.id
    session.flush()
    return charge


def update_profile(session, data):
    profile = session.get(Profile, 1, with_for_update=True)
    if profile is None:
        profile = Profile(id=1)
        session.add(profile)
    for key, label in (("minimum", "o saldo mínimo"), ("balance", "o saldo"),
                       ("commitments", "os compromissos"), ("reserve", "a reserva")):
        if key in data:
            value = data[key]
            setattr(profile, key, None if value in (None, "") else money(value, label, signed=key == "balance"))
    if "next_income_date" in data:
        value = data["next_income_date"]
        profile.next_income_date = _day(value, "a próxima entrada") if value else None
    if "goal" in data:
        profile.goal = _text(data["goal"], "objetivo", limit=500) or None
    if "horizon_months" in data:
        value = data["horizon_months"]
        profile.horizon_months = None if value in (None, "") else _integer(value, "o prazo")
    if "risk" in data:
        value = data["risk"]
        if value not in ("conservative", "moderate", "aggressive", "", None):
            raise ValueError("Escolha a tolerância a perdas.")
        profile.risk = value or None
    if "targets" in data:
        targets = data["targets"]
        if targets is None:
            profile.targets = None
        else:
            if not isinstance(targets, dict) or not 1 <= len(targets) <= 100:
                raise ValueError("Distribua as metas entre as classes da carteira.")
            clean = {_text(k, "classe", required=True): money(v, "o percentual") for k, v in targets.items()}
            if sum(clean.values()) != HUNDRED or len(clean) != len(targets):
                raise ValueError("As metas por classe precisam somar 100%.")
            profile.targets = {k: str(v) for k, v in clean.items()}
    session.flush()
    return profile


def _targets(profile):
    if profile.targets:
        return {k: Decimal(v) for k, v in profile.targets.items()}, "Você definiu os percentuais por classe."
    if any(value is None for value in (profile.risk, profile.reserve, profile.horizon_months, profile.goal)):
        return {}, "Informe objetivo, prazo, reserva e tolerância a perdas para calcular as metas."
    if profile.horizon_months <= 24 or profile.reserve == 0:
        reason = ("O prazo é de até dois anos." if profile.horizon_months <= 24
                  else "Você informou que ainda está formando a reserva.")
        return {CLASSES[0]: HUNDRED}, reason + " A simulação prioriza renda fixa. Confira a liquidez de cada posição."
    weights = {"conservative": (80, 5, 5, 10), "moderate": (55, 20, 10, 15),
               "aggressive": (30, 35, 10, 25)}
    risk_label = {"conservative": "baixa", "moderate": "moderada", "aggressive": "alta"}[profile.risk]
    return dict(zip(CLASSES, map(Decimal, weights[profile.risk]))), (
        f"A simulação considera o prazo de {profile.horizon_months} meses e tolerância {risk_label} a perdas. "
        "Você pode ajustar cada percentual."
    )


def _cash(value):
    if value is None:
        return None
    rounded = value.quantize(CENT, rounding=ROUND_HALF_UP)
    return format(rounded if rounded else ZERO, "f")


def _ratio(part, total):
    return part * HUNDRED / total if total else ZERO


def allocate(values, targets, amount):
    if not targets:
        return []
    classes = sorted(set(values) | set(targets))
    projected_total = sum(values.values(), ZERO) + amount
    gaps = {k: max(ZERO, projected_total * targets.get(k, ZERO) / HUNDRED - values.get(k, ZERO))
            for k in classes}
    gap_total = sum(gaps.values(), ZERO)
    shares = {k: (amount * gaps[k] / gap_total if gap_total else ZERO) for k in classes}
    parts = {k: share.quantize(CENT, rounding=ROUND_FLOOR) for k, share in shares.items()}
    remainder = int((amount - sum(parts.values(), ZERO)) / CENT)
    for k in sorted(classes, key=lambda k: shares[k] - parts[k], reverse=True)[:remainder]:
        parts[k] += CENT
    return [{"asset_class": k, "target_percent": _cash(targets.get(k, ZERO)),
             "current_value": _cash(values.get(k, ZERO)), "amount": _cash(parts[k]),
             "percent": _cash(_ratio(parts[k], amount)),
             "projected_value": _cash(values.get(k, ZERO) + parts[k]),
             "projected_percent": _cash(_ratio(values.get(k, ZERO) + parts[k], projected_total))}
            for k in classes]


def _effect(entry):
    if entry.advanced_by is None:
        if entry.kind == "expense":
            return entry.amount
        if entry.kind == "refund":
            return -entry.amount
    return ZERO


def entry_data(entry):
    return {"id": entry.id, "external_id": entry.external_id, "description": entry.description,
            "original_description": entry.original_description, "original_kind": entry.original_kind,
            "original_category": entry.original_category, "merchant": entry.merchant, "scheduled": entry.scheduled,
            "amount": _cash(entry.amount), "kind": entry.kind, "bank_direction": entry.bank_direction, "category": entry.category,
            "date": entry.booked_on.isoformat(), "source": entry.source, "effect": _cash(_effect(entry)),
            "classification_source": entry.classification_source, "installment_group": entry.installment_group,
            "installment_index": entry.installment_index, "advanced_by": entry.advanced_by}


def position_data(position):
    updated = position.updated_at
    if updated.tzinfo is None:
        updated = updated.replace(tzinfo=timezone.utc)
    return {"id": position.id, "external_id": position.external_id, "name": position.name,
            "identifier": position.identifier, "asset_class": position.asset_class, "issuer": position.issuer,
            "indexer": position.indexer, "maturity": position.maturity.isoformat() if position.maturity else None,
            "institution": position.institution, "value": _cash(position.value), "currency": position.currency,
            "active": position.active, "source": position.source, "updated_at": updated.isoformat(),
            "needs_review": position.needs_review, "field_sources": position.field_sources, "conflicts": position.conflicts}


def _previous_month(start):
    return (start - timedelta(days=1)).replace(day=1)


def _spending(session, start, today):
    last_day = calendar.monthrange(start.year, start.month)[1]
    cutoff = min(start.replace(day=last_day), today)
    future = start > today
    previous = start
    months = []
    for _ in range(3):
        previous = _previous_month(previous)
        months.append(previous)
    entries = list(session.scalars(select(Entry).where(Entry.active.is_(True), Entry.booked_on.between(previous, start.replace(day=last_day)))
                                   .order_by(Entry.booked_on.desc(), Entry.id.desc())))
    current = [r for r in entries if start <= r.booked_on <= cutoff and not r.advanced_by]
    elapsed = cutoff.day if not future else 0
    history, historic_categories, historic_merchants = [], defaultdict(Decimal), defaultdict(Decimal)
    for prior in months:
        last = calendar.monthrange(prior.year, prior.month)[1]
        end = prior.replace(day=min(last, max(1, elapsed)))
        recorded = [r for r in entries if prior <= r.booked_on <= prior.replace(day=last) and not r.advanced_by]
        if future or not recorded:
            continue
        total = ZERO
        for row in recorded:
            if row.booked_on <= end:
                effect = _effect(row)
                total += effect
                if effect:
                    historic_categories[row.category] += effect
                    historic_merchants[(row.category, row.merchant or row.original_description)] += effect
        history.append({"month": prior.strftime("%Y-%m"), "through": end.isoformat(), "total": _cash(total)})
    totals, purchases, merchant_totals = defaultdict(Decimal), defaultdict(list), defaultdict(Decimal)
    for row in current:
        effect = _effect(row)
        if effect:
            totals[row.category] += effect
            merchant_totals[(row.category, row.merchant or row.original_description)] += effect
            purchases[row.category].append(entry_data(row))
    merchants = defaultdict(list)
    for cat, name in sorted(set(merchant_totals) | set(historic_merchants)):
        current_total = merchant_totals[(cat, name)]
        mean = historic_merchants[(cat, name)] / len(history) if history else None
        merchants[cat].append({"merchant": name, "total": _cash(current_total), "average": _cash(mean),
                               "change": _cash(current_total - mean if history else None)})
    categories = []
    for key in sorted(set(totals) | set(historic_categories)):
        average = historic_categories[key] / len(history) if history else None
        categories.append({"category": key, "total": _cash(totals[key]), "average": _cash(average),
                           "change": _cash(totals[key] - average if history else None),
                           "merchants": sorted(merchants[key], key=lambda r: abs(Decimal(r["change"] or r["total"])), reverse=True),
                           "entries": sorted(purchases[key], key=lambda r: abs(Decimal(r["effect"])), reverse=True)})
    categories.sort(key=lambda row: Decimal(row["total"]), reverse=True)
    total = sum(totals.values(), ZERO)
    average = sum(Decimal(row["total"]) for row in history) / len(history) if history else None
    difference = total - average if average is not None else None
    installments = session.scalars(select(Entry).where(Entry.active.is_(True), Entry.advanced_by.is_(None), Entry.kind == "expense",
                                                        Entry.installment_group.is_not(None), Entry.booked_on >= today)
                                   .order_by(Entry.booked_on, Entry.id))
    pending_charges = {row.id: row for row in current if row.source == "advance"}
    links = defaultdict(list)
    if pending_charges:
        for entry_id, charge_id in session.execute(select(Entry.id, Entry.advanced_by)
                                                   .where(Entry.advanced_by.in_(pending_charges.keys()))
                                                   .order_by(Entry.id)):
            links[charge_id].append(entry_id)
    advances = [dict(id=row.id, date=row.booked_on.isoformat(), amount=_cash(row.amount),
                     description=row.description, entry_ids=links[row.id], source=row.source)
                for row in pending_charges.values()]
    return {"total": _cash(total), "cutoff": cutoff.isoformat(), "history": history, "average": _cash(average),
            "change": _cash(difference), "contribution_impact": _cash(-difference if history else None),
            "categories": categories, "entries": [entry_data(e) for e in current], "advances": advances,
            "pending": sum(e.classification_source == "pending" for e in current),
            "installments": [entry_data(e) for e in installments]}


def _budget(session, profile, bank, today, amount, values, targets):
    balance = profile.balance if profile.balance is not None else bank.get("balance")
    balance_source = "manual" if profile.balance is not None else ("bank" if balance is not None else None)
    until = profile.next_income_date
    commitments = profile.commitments
    source = "manual" if commitments is not None else None
    breakdown = []
    uncertain = []
    warnings = []
    balance_fresh = balance_source == "manual" or bank.get("balance_fresh") is True
    forecast_complete = source == "manual" or bank.get("forecast_complete") is True
    if balance_source == "bank" and not balance_fresh:
        warnings.append("O último saldo bancário precisa de atualização. Você pode informar o saldo de hoje em Ajustar.")
    if commitments is None and until is not None and until >= today:
        bills = bank.get("bills", ())
        bill_ids = {bill["id"] for bill in bills}
        for bill in bills:
            if bill["due"] <= until and bill["amount"] > 0:
                label = "Fatura vencida" if bill["due"] < today else "Fatura do cartão"
                breakdown.append({"description": label, "date": bill["due"].isoformat(), "amount": _cash(bill["amount"])})
        forecast = session.scalars(select(Entry).where(Entry.active.is_(True), Entry.booked_on.between(today, until), Entry.kind == "expense",
                                                      Entry.advanced_by.is_(None)).order_by(Entry.booked_on, Entry.id))
        for row in forecast:
            if row.bill_id and row.bill_id in bill_ids:
                continue
            item = {"description": row.description, "date": row.booked_on.isoformat(), "amount": _cash(row.amount)}
            if row.card and not row.bill_id and row.account_id:
                same_card = [bill for bill in bills if row.account_id == bill["account_id"]]
                if any(row.booked_on == bill["due"] for bill in same_card):
                    continue
                if any(row.booked_on < bill["due"] <= until for bill in same_card):
                    uncertain.append(item)
                    continue
            if row.booked_on == today and not row.scheduled and not row.card:
                # O saldo informado já reflete débitos efetivados hoje.
                continue
            breakdown.append(item)
        if breakdown or forecast_complete:
            commitments = sum((Decimal(item["amount"]) for item in breakdown), ZERO)
            source = "forecast"
        if not forecast_complete:
            warnings.append("A conexão ainda não confirmou todas as contas do período. Confira o extrato e informe o total em Ajustar.")
    known = (balance_fresh and forecast_complete and not uncertain
             and all(value is not None for value in (balance, commitments, profile.minimum)))
    if until and until < today:
        known = False
        warnings.append("A data prevista para a próxima entrada já passou. Atualize a data em Ajustar.")
    net = balance - commitments - profile.minimum if known else None
    available = max(net, ZERO) if known else None
    amount = available if amount is None else money(amount, "o aporte simulado")
    return {"balance": _cash(balance), "balance_source": balance_source, "commitments": _cash(commitments),
            "commitments_source": source, "minimum": _cash(profile.minimum), "confirmed": known,
            "next_income_date": until.isoformat() if until else None, "available": _cash(available),
            "shortfall": _cash(max(-net, ZERO) if known else None), "simulated": _cash(amount),
            "breakdown": sorted(breakdown, key=lambda item: item["date"]), "uncertain_commitments": uncertain, "warnings": warnings,
            "allocation": allocate(values, targets, amount) if amount is not None else []}


def dashboard(session, month=None, amount=None, *, today=None, bank=None):
    today = today or today_local()
    start = _day(f"{month}-01", "o mês") if month is not None else today.replace(day=1)
    if start.year < 2:
        raise ValueError("Informe um mês a partir do ano 0002.")
    profile = session.get(Profile, 1) or Profile(id=1)
    targets, reason = _targets(profile)
    positions = list(session.scalars(select(Position).where(Position.active.is_(True))
                                    .order_by(Position.value.desc(), Position.id)))
    values, foreign = defaultdict(Decimal), defaultdict(Decimal)
    for row in positions:
        if row.currency == "BRL":
            values[row.asset_class] += row.value
        else:
            foreign[row.currency] += row.value
    total = sum(values.values(), ZERO)
    classes = [{"asset_class": k, "value": _cash(v), "percent": _cash(_ratio(v, total))}
               for k, v in sorted(values.items(), key=lambda item: -item[1])]
    profile_json = {"goal": profile.goal, "horizon_months": profile.horizon_months, "risk": profile.risk,
                    "reserve": _cash(profile.reserve), "balance": _cash(profile.balance),
                    "commitments": _cash(profile.commitments), "minimum": _cash(profile.minimum),
                    "next_income_date": profile.next_income_date.isoformat() if profile.next_income_date else None,
                    "targets": {k: _cash(v) for k, v in targets.items()}, "target_reason": reason,
                    "targets_manual": profile.targets is not None, "target_classes": sorted(set(CLASSES) | set(values) | set(targets))}
    return {"month": start.strftime("%Y-%m"), "as_of": today.isoformat(), "profile": profile_json,
            "portfolio": {"total": _cash(total), "classes": classes, "positions": [position_data(p) for p in positions],
                          "foreign_totals": {k: _cash(v) for k, v in foreign.items()}},
            "spending": _spending(session, start, today),
            "contribution": _budget(session, profile, bank or {}, today, amount, values, targets)}


def calculate(data):
    mode = data.get("mode")
    if mode not in ("compound", "time", "monthly"):
        raise ValueError("Escolha uma das três calculadoras.")
    principal = money(data.get("principal"), "o valor inicial")
    monthly_rate = "monthly_rate" in data
    if monthly_rate and "annual_rate" in data:
        raise ValueError("Informe uma única taxa de juros.")
    try:
        rate = Decimal(str(data.get("monthly_rate" if monthly_rate else "annual_rate")).replace(",", "."))
        if not rate.is_finite() or not -100 < rate <= 1000 or rate.as_tuple().exponent < -8:
            raise ValueError
    except (InvalidOperation, ValueError):
        period = "mensal" if monthly_rate else "anual"
        raise ValueError(f"Informe a taxa {period} em porcentagem, maior que -100% e de até 1.000%, "
                         "com até oito casas decimais.") from None
    payment = money(data.get("monthly"), "o aporte mensal") if mode != "monthly" else ZERO
    target = money(data.get("target"), "o valor da meta") if mode != "compound" else None
    with localcontext() as ctx:
        ctx.prec = 48
        rate_factor = 1 + rate / HUNDRED
        factor = rate_factor if monthly_rate else rate_factor ** (Decimal(1) / 12)
        annual_factor = factor ** 12 if monthly_rate else rate_factor
        equivalent = ((annual_factor if monthly_rate else factor) - 1) * HUNDRED
        details = {"equivalent_rate": f"{equivalent:.10f}"}
        if mode == "time":
            current = principal
            for n in range(1201):
                if current.quantize(CENT, rounding=ROUND_HALF_UP) >= target:
                    return {"value": n, "unit": "months", **details}
                next_value = current * factor + payment
                if next_value <= current:
                    break
                current = next_value
            raise ValueError("Com esses valores, a meta não é atingida em até 100 anos.")
        months = _integer(data.get("months"), "o prazo em meses", lower=0 if mode == "compound" else 1)
        years, remaining_months = divmod(months, 12)
        growth = annual_factor ** years * factor ** remaining_months
        annuity = (growth - 1) / (factor - 1) if factor != 1 else Decimal(months)
        if mode == "compound":
            answer = principal * growth + payment * annuity
        else:
            answer = max(ZERO, (target - principal * growth) / annuity)
        if answer > MAX_MONEY:
            raise ValueError("O resultado ultrapassa o limite de valores desta calculadora.")
        # Arredondar para cima garante que o aporte calculado alcança a meta.
        if mode == "monthly":
            answer = -(-answer).quantize(CENT, rounding=ROUND_FLOOR)
        else:
            invested = principal + payment * months
            details.update(invested=_cash(invested), earnings=_cash(answer - invested))
        return {"value": _cash(answer), "unit": "BRL", **details}
