import io
import re
from datetime import date
from decimal import Decimal

import pytest
from sqlalchemy import select

from openchris import banking
from openchris.db import db_session, initialize
from openchris.finance import Entry, Position
from openchris.web import create_app


@pytest.fixture()
def client(tmp_path, monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "sqlite:///" + str(tmp_path / "test.db"))
    monkeypatch.setenv("OPENCHRIS_STATE", str(tmp_path))
    return create_app({"TESTING": True, "OWNER_PASSWORD": "testing-only-6475", "SESSION_COOKIE_SECURE": False}).test_client()


def login(client):
    page = client.get("/login")
    token = re.search(r'name="csrf_token" value="([^"]+)', page.text)[1]
    assert client.post("/login", data={"password": "testing-only-6475", "csrf_token": token}).status_code == 302
    with client.session_transaction() as s:
        return {"X-CSRF-Token": s["csrf"]}


def test_deep_link_and_csv_replay(client):
    assert client.get("/api/finance").status_code == 401
    assert "next=" in client.get("/economia?evento=um").location
    headers = login(client)
    assert client.post("/api/import").status_code == 403
    data = "external_id;name;value\na;Título A;1.234,56\n".encode()

    def upload(contents):
        return client.post("/api/import", data={"file": (io.BytesIO(contents), "carteira.csv"), "kind": "positions"},
                           headers=headers)

    assert upload(data).json["count"] == 1
    assert upload(data).status_code == 200
    assert upload(data.replace(b"1.234,56", b"10,00") + b"b;Outro;abc\n").status_code == 400
    with db_session() as db:
        positions = db.scalars(select(Position)).all()
        assert [p.value for p in positions] == [Decimal("1234.56")]
    assert client.post("/logout", headers=headers).status_code == 303
    assert client.get("/api/finance").status_code == 401


def test_provider_cursor_stays_on_fixed_endpoint(monkeypatch):
    calls = []

    def api(method, path, **kwargs):
        calls.append(path)
        return {"results": [len(calls)], "next": "?accountId=A&after=X" if len(calls) == 1 else None}

    monkeypatch.setattr(banking, "_api", api)
    assert list(banking._records("/v2/transactions", {"accountId": "A"}, cursor=True)) == [1, 2]
    assert calls == ["/v2/transactions", "/v2/transactions?accountId=A&after=X"]


def test_repeated_bank_read_and_partial_snapshot(client, monkeypatch):
    item = "8460bfda-c44c-449c-9006-e84881fe5fc6"
    with db_session() as db:
        db.add(banking.Connection(id=item))
    meta = {"id": item, "status": "UPDATED", "clientUserId": "openchris-personal"}
    monkeypatch.setattr(banking, "_read_item", lambda _: meta)
    accounts = [{"id": "a", "type": "BANK", "currencyCode": "BRL", "balance": 1000},
                {"id": "b", "type": "CREDIT", "currencyCode": "BRL", "balance": 120}]
    bill = {"id": "bill", "dueDate": "2026-12-03", "totalAmount": 120, "totalAmountCurrencyCode": "BRL",
            "payments": [{"amount": 20, "currencyCode": "BRL"}]}
    tx = {"id": "x", "description": "Mercado", "date": "2026-11-11", "currencyCode": "BRL",
          "amount": 120, "creditCardMetadata": {"billId": "bill"}}
    investments = [{"id": "i", "code": "ex1", "name": "Título", "currencyCode": "BRL", "type": "FIXED_INCOME",
                    "balance": 550, "status": "ACTIVE"}]

    def records(path, params, **kwargs):
        if path == "/v2/transactions":
            return [tx] if params["accountId"] == "b" else []
        return {"/accounts": accounts, "/bills": [bill], "/investments": investments}[path]

    monkeypatch.setattr(banking, "_records", records)
    banking.sync_item(item)
    banking.sync_item(item)
    meta["status"] = "PARTIAL_SUCCESS"
    investments.clear()
    banking.sync_item(item)
    with db_session() as db:
        cash = banking.bank_snapshot(db)
        assert cash["balance"] == 1000
        assert cash["bills"][0]["amount"] == 100
        position = db.scalars(select(Position)).one()
        assert position.active and position.value == 550
        entry = db.scalars(select(Entry)).one()
        assert (entry.booked_on, entry.kind) == (date(2026, 12, 3), "expense")
        assert not cash["balance_fresh"] and not cash["forecast_complete"]


def test_meu_pluggy_uses_account_codes_and_investment_holders(client, monkeypatch):
    items = {str(n): label for n, label in enumerate(("Nubank", "Itaú", "BTG Pactual", "", "Corretora Exemplo", "Banco Direto"))}
    with db_session() as db:
        db.add_all(banking.Connection(id=item, name="MeuPluggy") for item in items)

    def metadata(item):
        return {"status": "UPDATED", "products": ["ACCOUNTS", "INVESTMENTS"],
                "connector": {"name": "Banco Direto" if item == "5" else "MeuPluggy"}}

    def records(path, params, **_kwargs):
        item = params.get("itemId")
        if path == "/accounts":
            code = {"0": "260", "1": "341", "2": "208", "5": "260"}.get(item)
            return [{"id": f"a:{item}", "type": "BANK", "balance": 10, "currencyCode": "BRL",
                     "bankData": {"transferNumber": f"{code}/0001/111-1"}}] if code else []
        if path == "/investments" and item != "3":
            return [{"id": f"i:{item}", "name": "Título", "currencyCode": "BRL", "type": "FIXED_INCOME", "balance": 50,
                     "issuer": "Emissor Teste", "institution": {"name": "Corretora Exemplo"} if item == "4" else None}]
        return []

    monkeypatch.setattr(banking, "_read_item", metadata)
    monkeypatch.setattr(banking, "_records", records)
    for i in items:
        banking.sync_item(i)

    login(client)
    result = {row["id"]: row for row in client.get("/api/connections").json["items"]}
    assert {key: row["name"] for key, row in result.items()} == items
    assert {key for key, row in result.items() if row["no_data"]} == {"3"}
    assert client.get("/api/finance").json["portfolio"]["missing_connections"] == 1
    with db_session() as db:
        assert {p.external_id: p.institution for p in db.scalars(select(Position))} == {
            f"pluggy:i:{key}": name for key, name in items.items() if name}


def test_meu_pluggy_resolves_other_codes_from_banco_central(monkeypatch):
    class DirectoryResponse:
        content = "Número_Código,Nome_Reduzido\n348,Banco XP\n102,XP Investimentos\n".encode("utf-8-sig")

        def raise_for_status(self):
            pass

    def directory(url, **_kwargs):
        assert url == banking.BANK_DIRECTORY
        return DirectoryResponse()

    monkeypatch.setattr(banking.requests, "get", directory)
    banking._bank_directory.cache_clear()
    try:
        bank = {"type": "BANK", "bankData": {"transferNumber": "348/0001/111-1"}}
        broker = {"type": "BANK", "bankData": {"transferNumber": "102/0001/222-2"}}
        assert banking._connection_institutions("Meu Pluggy", [bank], []) == ("Banco XP", "Banco XP")
        assert banking._connection_institutions("Meu Pluggy", [bank, broker], []) == ("Banco XP, XP Investimentos", "")
    finally:
        banking._bank_directory.cache_clear()


@pytest.mark.parametrize("bank_first", [True, False])
@pytest.mark.parametrize("csv_id", ["i", "outro-id"])
def test_csv_and_provider_are_one_holding(client, bank_first, csv_id):
    values = {"name": "CDB A", "identifier": "CDB-AB12", "institution": "Banco Exemplo", "value": 100}
    steps = [("csv", csv_id), ("pluggy", "pluggy:i")]
    if bank_first:
        steps.reverse()
    with db_session() as db:
        for source, key in steps + steps:
            banking._import_row(db, "positions", {**values, "external_id": key}, source)
        holding = db.scalars(select(Position)).one()
        assert (holding.value, holding.source, holding.external_id) == (100, "pluggy", "pluggy:i")


def test_ambiguous_csv_match_does_not_merge_two_bank_purchases(client):
    values = {"description": "Loja", "amount": 25, "booked_on": "2026-11-01", "kind": "expense"}
    with db_session() as db:
        for key in ("pluggy:x1", "pluggy:x2"):
            banking._import_row(db, "entries", {**values, "external_id": key}, "pluggy")
        with pytest.raises(ValueError, match="Mais de um registro"):
            banking._import_row(db, "entries", {**values, "external_id": "csv:outro"}, "csv")
        assert len(list(db.scalars(select(Entry)))) == 2


def test_bank_replay_removes_internal_movements_from_expenses_and_income(client):
    def party(number):
        return {"documentNumber": {"type": "CPF", "value": number}}

    internal = {"paymentMethod": "PIX", "payer": party("123.456.789-09"),
                "receiver": party("12345678909")}
    external = {**internal, "receiver": party("98765432100")}
    cases = [
        ("sent", -80, "PIX enviado", "Same person transfer", None, "expense", "transfer"),
        ("received", 80, "PIX recebido", "Transfers", internal, "income", "transfer"),
        ("other", -80, "PIX enviado", "Transfers", external, "expense", "expense"),
        ("bill", -120, "Pagamento de fatura", "Transfers", None, "expense", "bill_payment"),
        ("store", -20, "Pagamento de fatura de internet", "Transfers", None, "expense", "expense"),
    ]
    with db_session() as db:
        for key, amount, description, category, payment, previous, expected in cases:
            raw = {"id": key, "amount": amount, "description": description, "category": category,
                   "paymentData": payment, "date": "2025-11-08"}
            account = {"id": "destination" if amount > 0 else "origin", "type": "BANK", "currency": "BRL"}
            row = banking._entry(raw, account, {})
            stored = banking._import_row(db, "entries", {**row, "kind": previous}, "pluggy")
            stored.classification_source = "codex"
            for _ in range(2):
                current = banking._import_row(db, "entries", row, "pluggy")
                assert (current.id, current.kind) == (stored.id, expected)
        assert len(db.scalars(select(Entry)).all()) == len(cases)

    login(client)
    spending = client.get("/api/finance?month=2025-11").json["spending"]
    assert spending["total"] == "100.00"


def test_bank_replay_recognizes_broker_yield_and_preserves_manual_classification(client):
    account = {"id": "broker", "type": "BANK", "currency": "BRL"}
    raw = {"id": "yield", "amount": 7.63, "description": "Entrada  COR RENDIMENTO RECR11",
           "category": "Investments", "date": "2025-11-08"}
    with db_session() as db:
        row = banking._entry(raw, account, {})
        assert (row["kind"], row["category"], row["bank_direction"]) == (
            "income", "Rendimentos de investimentos", "inflow")
        saved = banking._import_row(db, "entries", {**row, "kind": "transfer", "category": ""}, "pluggy")
        saved.classification_source = "codex"
        latest = banking._import_row(db, "entries", row, "pluggy")
        assert (latest.id, latest.kind, latest.category) == (saved.id, "income", row["category"])
        latest.kind, latest.category, latest.classification_source = "transfer", "Revisada", "manual"
        latest = banking._import_row(db, "entries", row, "pluggy")
        assert (latest.kind, latest.category) == ("transfer", "Revisada")

    for change in ({"amount": -5}, {"description": "Entrada COR RENDIMENTO"}, {"description": "Resgate de investimento"}):
        assert banking._entry({**raw, **change}, account, {})["kind"] == "transfer"


def test_card_installment_discounts_reduce_spending_and_update_prior_imports(client):
    card = {"id": "card", "type": "CREDIT", "currency": "BRL"}
    raw = {"id": "credit-1", "amount": -2.17, "category": "", "description": "Desc Antecipa Parcelas",
           "date": "2025-11-08"}
    with db_session() as db:
        current = banking._import_row(db, "entries", banking._entry(raw, card, {}), "pluggy")
        assert (current.kind, current.category, current.bank_direction, current.original_kind) == (
            "refund", "Desconto de antecipação", "inflow", "unknown")
        for key, account, changes in (
            ("credit-2", card, {"description": " DESC   ANTECIPA PARCELAS  "}),
            ("changed-manually", card, {}),
            ("debit", card, {"amount": 2.17}),
            ("credit-without-card", {**card, "type": "BANK"}, {"amount": 2.17}),
        ):
            row = banking._import_row(db, "entries", banking._entry({**raw, **changes, "id": key}, account, {}), "pluggy")
            row.kind, row.category, row.classification_source = (
                ("transfer", "Revisada", "manual") if key == "changed-manually" else ("unknown", "Sem categoria", "pending"))

    initialize()
    initialize()
    with db_session() as db:
        entries = {e.external_id: e for e in db.scalars(select(Entry))}
        assert entries["pluggy:credit-2"].kind == "refund"
        assert entries["pluggy:changed-manually"].kind == "transfer"
        assert entries["pluggy:debit"].kind == entries["pluggy:credit-without-card"].kind == "unknown"
        imported = banking._import_row(db, "entries", banking._entry(raw, card, {}), "pluggy")
        assert imported.id == entries["pluggy:credit-1"].id

    login(client)
    spending = client.get("/api/finance?month=2025-11").json["spending"]
    assert spending["total"] == "-4.34"
