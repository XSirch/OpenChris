import unittest
from contextlib import contextmanager
from datetime import date
from decimal import Decimal
from unittest.mock import patch

from flask import Flask
from sqlalchemy import create_engine, func, select
from sqlalchemy.orm import Session

from openchris.db import Base
from openchris.finance_api import bp
from openchris.finance import (
    Entry, Profile, advance_installments, allocate, apply_position_facts, calculate, classify_entry,
    correct_entry, dashboard, money, update_profile, upsert_entry, upsert_position,
)


class FinanceTest(unittest.TestCase):
    def setUp(self):
        self.engine = create_engine("sqlite://")
        Base.metadata.create_all(self.engine)
        self.db = Session(self.engine, expire_on_commit=False)
        self.addCleanup(self.engine.dispose)
        self.addCleanup(self.db.close)

    def add_entry(self, key, day, amount="100", **changes):
        fields = {"external_id": key, "description": "Compra", "merchant": "Armazém",
                  "amount": amount, "category": "Mercado", "kind": "expense", "date": day, **changes}
        return upsert_entry(self.db, fields, source="pluggy")

    def summary(self, day="2025-02-12", **kwargs):
        return dashboard(self.db, today=date.fromisoformat(day), **kwargs)

    def test_same_period_history_refunds_and_merchant_evidence(self):
        self.add_entry("one", "2025-02-01", "90.35")
        self.add_entry("refund", "2025-02-04", "10", kind="refund")
        self.add_entry("bill", "2025-02-03", "4000", kind="bill_payment")
        self.add_entry("transfer", "2025-02-03", "4000", kind="transfer")
        self.add_entry("next", "2025-02-18", "100")
        self.add_entry("unknown", "2025-02-03", "110", kind="unknown")
        self.add_entry("jan", "2025-01-08", "40")
        self.add_entry("jan-late", "2025-01-13", "400")
        spending = self.summary()["spending"]
        self.assertEqual(spending["total"], "80.35")
        self.assertEqual(spending["average"], "40.00")
        self.assertEqual(spending["contribution_impact"], "-40.35")
        self.assertEqual(spending["pending"], 1)
        self.assertEqual(spending["history"], [{"month": "2025-01", "through": "2025-01-12", "total": "40.00"}])
        self.assertEqual(spending["categories"][0]["merchants"][0]["change"], "40.35")
        self.assertEqual(self.summary(day="2025-02-28")["spending"]["history"][0]["through"], "2025-01-28")
        self.assertIsNone(self.summary(month="2024-04")["spending"]["average"])

    def test_advance_discount_and_reimport_preserve_originals(self):
        group = {"installment_group": "armazem-cartao-1", "account_id": "cartao", "card": True}
        old = self.add_entry("paid", "2025-01-12", **group, installment_index=1)
        second = self.add_entry("future2", "2025-02-18", **group, installment_index=2)
        third = self.add_entry("future3", "2025-03-18", **group, installment_index=3)
        fields = {"entry_ids": [second.id, third.id], "date": "2025-02-10", "amount": "185.10"}
        synth = advance_installments(self.db, fields)
        self.assertEqual(advance_installments(self.db, fields).id, synth.id)
        self.add_entry("future2", "2025-02-18", **group, installment_index=2)
        self.assertEqual(second.advanced_by, synth.id)
        self.assertEqual(self.summary()["spending"]["total"], "185.10")
        self.assertEqual(self.summary()["spending"]["advances"][0]["entry_ids"], [second.id, third.id])
        self.assertEqual(self.summary(month="2025-01")["spending"]["total"], "100.00")
        self.assertEqual(self.summary(day="2025-04-01", month="2025-03")["spending"]["total"], "0.00")
        charge = self.add_entry("bank-charge", "2025-02-10", "185.10", kind="unknown", account_id="cartao")
        self.add_entry("bill-pay", "2025-02-10", "185.10", kind="bill_payment")
        third.active = False  # A parcela futura pode desaparecer do banco depois da antecipação.
        self.assertEqual(advance_installments(self.db, dict(fields, charge_entry_id=charge.id)).id, charge.id)
        self.assertEqual(advance_installments(self.db, fields).id, charge.id)
        self.assertEqual(self.summary()["spending"]["total"], "185.10")
        self.assertEqual(self.db.get(Entry, old.id).amount, Decimal(100))
        with self.assertRaisesRegex(ValueError, "vencimento"):
            advance_installments(self.db, dict(fields, entry_ids=[old.id]))
        with self.assertRaisesRegex(ValueError, "diferente"):
            advance_installments(self.db, dict(fields, entry_ids=[second.id]))

    def test_corrections_survive_sync_and_classification(self):
        a = self.add_entry("id", "2025-02-01", kind="unknown", category="")
        existing = self.add_entry("existing", "2025-01-02", kind="unknown", category="")
        self.assertTrue(classify_entry(self.db, a.id, "expense", "Mercado"))
        correct_entry(self.db, {"entry_id": a.id, "kind": "expense", "category": "Alimentação"})
        self.assertEqual((existing.classification_source, existing.category), ("rule", "Alimentação"))
        self.assertFalse(classify_entry(self.db, a.id, "transfer", "Outra"))
        self.add_entry("id", "2025-02-01", kind="unknown", category="", description="Texto atualizado")
        self.assertEqual((a.category, a.original_description, a.original_kind), ("Alimentação", "Compra", "unknown"))
        b = self.add_entry("another", "2025-02-04", kind="unknown", category="")
        self.assertEqual((b.classification_source, b.category), ("rule", "Alimentação"))
        refund = self.add_entry("refund", "2025-02-04", kind="refund")
        self.assertEqual(refund.kind, "refund")
        a.active = False
        self.assertEqual(self.summary()["spending"]["total"], "0.00")
        a = self.add_entry("id", "2025-02-01", kind="unknown", category="")
        self.assertTrue(a.active)
        self.assertEqual(self.db.scalar(select(func.count()).select_from(Entry)), 4)

    def test_snapshot_values_and_sources(self):
        p = upsert_position(self.db, {"external_id": "p", "name": "Letra",
                                      "value": "120.55", "updated_at": "2025-02-12"}, "pluggy")
        self.assertTrue(p.needs_review)
        self.assertTrue(apply_position_facts(self.db, p.id, {"asset_class": "Renda fixa", "issuer": "Banco A"},
                                             "https://example.org/ficha"))
        self.assertFalse(apply_position_facts(self.db, p.id, {"issuer": "Banco B"}, "outra fonte"))
        self.assertFalse(apply_position_facts(self.db, p.id, {"indexer": "CDI"}, "ficha"))
        upsert_position(self.db, {"external_id": "p", "issuer": "Banco A"}, "manual")
        self.assertFalse(p.needs_review)
        self.assertEqual(p.field_sources["issuer"], "manual")
        upsert_position(self.db, {"external_id": "p", "value": "23", "updated_at": "2025-02-01"}, "pluggy")
        self.assertEqual(p.value, Decimal("120.55"))
        upsert_position(self.db, {"external_id": "foreign", "name": "Papel", "value": "23.90", "currency": "USD"})
        upsert_position(self.db, {"external_id": "closed", "name": "Outro", "value": "1000", "active": False})
        data = self.summary()["portfolio"]
        self.assertEqual(data["total"], "120.55")
        self.assertEqual(data["foreign_totals"], {"USD": "23.90"})
        self.assertEqual(len(data["positions"]), 2)

    def test_etf_target_and_variable_income_per_currency(self):
        for key, asset_class, value, currency in (
            ("etf", "ETFs", "60", "BRL"), ("stock", "Ações", "20", "BRL"),
            ("fixed", "Renda fixa", "420", "BRL"), ("etf-us", "ETFs", "10", "USD"),
        ):
            upsert_position(self.db, dict(external_id=key, name=key, asset_class=asset_class,
                                          value=value, currency=currency))
        update_profile(self.db, {"targets": {"ETFs": "20.25", "Renda fixa": "79.75"}})
        result = self.summary(amount="100")
        self.assertEqual(result["profile"]["targets"]["ETFs"], "20.25")
        self.assertEqual(result["portfolio"]["total"], "500.00")
        self.assertEqual(result["portfolio"]["variable_income"], {"BRL": "80.00", "USD": "10.00"})
        etf = next(row for row in result["contribution"]["allocation"] if row["asset_class"] == "ETFs")
        self.assertEqual(etf["target_percent"], "20.25")

    def test_merchant_rules_and_corrections_respect_bank_direction(self):
        fields = {"kind": "unknown", "category": ""}
        a = self.add_entry("debit", "2025-02-02", bank_direction="outflow", **fields)
        b = self.add_entry("credit", "2025-02-02", bank_direction="inflow", **fields)
        correct_entry(self.db, {"entry_id": a.id, "kind": "expense", "category": "Mercado"})
        self.assertEqual((b.kind, b.classification_source), ("unknown", "pending"))
        self.assertFalse(classify_entry(self.db, b.id, "expense", "Mercado"))
        for kind in ("income", "refund"):
            with self.assertRaisesRegex(ValueError, "contradiz"):
                correct_entry(self.db, {"entry_id": a.id, "kind": kind, "category": "Mercado"})
        with self.assertRaisesRegex(ValueError, "contradiz"):
            correct_entry(self.db, {"entry_id": b.id, "kind": "expense", "category": "Mercado"})
        self.assertEqual(b.kind, "unknown")
        correct_entry(self.db, {"entry_id": b.id, "kind": "refund", "category": "Mercado"})
        for direction, kind in (("inflow", "refund"), ("outflow", "expense")):
            entry = self.add_entry(direction, "2025-02-03", bank_direction=direction, **fields)
            self.assertEqual((entry.kind, entry.classification_source), (kind, "rule"))
        invalid = self.add_entry("bad-source", "2025-02-04", bank_direction="outflow", kind="income")
        self.assertEqual((invalid.original_kind, invalid.kind), ("income", "unknown"))
        self.assertTrue(classify_entry(self.db, invalid.id, "transfer", "Transferência"))
        correct_entry(self.db, {"entry_id": b.id, "kind": "bill_payment", "category": "Fatura"})
        self.assertEqual(b.kind, "bill_payment")

    def test_budget_bills_and_partial_data(self):
        self.assertIsNone(self.summary()["contribution"]["available"])
        update_profile(self.db, {"minimum": "50", "next_income_date": "2025-02-20"})
        bank = {"balance": Decimal("170"), "balance_fresh": True, "forecast_complete": True, "bills": [{"id": "bill", "account_id": "card",
                 "due": date(2025, 2, 18), "amount": Decimal("90")}]}
        self.add_entry("onbill", "2025-02-18", "60", card=True, bill_id="bill", account_id="card")
        self.add_entry("onbill2", "2025-02-18", "30", card=True, account_id="card")
        self.add_entry("outside", "2025-02-21", "100")
        self.add_entry("settled", "2025-02-12", "80")
        self.add_entry("scheduled", "2025-02-18", "40")
        result = self.summary(bank=bank)["contribution"]
        self.assertEqual((result["available"], result["shortfall"]), ("0.00", "10.00"))
        self.assertEqual(len(result["breakdown"]), 2)
        self.add_entry("ambiguous-card", "2025-02-15", "25", card=True, account_id="card")
        uncertain = self.summary(bank=bank)["contribution"]
        self.assertIsNone(uncertain["available"])
        self.assertEqual(uncertain["uncertain_commitments"][0]["amount"], "25.00")
        update_profile(self.db, {"commitments": "20", "balance": "300"})
        self.assertEqual(self.summary(bank=bank)["contribution"]["available"], "230.00")
        update_profile(self.db, {"commitments": None, "balance": None})
        self.assertEqual(self.summary(bank=bank)["contribution"]["balance_source"], "bank")
        self.assertIsNone(self.summary(day="2025-02-25", bank=bank)["contribution"]["available"])

    def test_overdue_bill_reserves_only_its_outstanding_amount(self):
        update_profile(self.db, {"balance": "210", "minimum": "30", "next_income_date": "2025-02-20"})
        bank = {"forecast_complete": True, "bills": [
            {"id": "overdue", "account_id": "a", "due": date(2025, 2, 7), "amount": Decimal("65.47")},
            {"id": "settled", "account_id": "a", "due": date(2025, 1, 7), "amount": Decimal("0")},
            {"id": "later", "account_id": "b", "due": date(2025, 3, 7), "amount": Decimal("700")},
        ]}
        for bill_id in ("overdue", "settled", "later"):
            self.add_entry(bill_id, "2025-02-13", "80", card=True, account_id="a", bill_id=bill_id)
        result = self.summary(bank=bank)["contribution"]
        self.assertEqual(result["available"], "114.53")
        self.assertEqual(result["breakdown"], [
            {"description": "Fatura vencida", "date": "2025-02-07", "amount": "65.47"}])

    def test_unconfirmed_bank_values_stay_visible_and_can_be_overridden(self):
        update_profile(self.db, {"minimum": "20", "next_income_date": "2025-02-20", "targets": {"Renda fixa": 100}})
        bank = {"balance": Decimal("500"), "balance_fresh": True, "forecast_complete": False}
        result = self.summary(bank=bank)["contribution"]
        self.assertIsNone(result["available"])
        self.assertIsNone(result["commitments"])
        self.assertEqual(result["balance"], "500.00")
        self.add_entry("pending", "2025-02-19", "30")
        partial = self.summary(bank=bank, amount="12")["contribution"]
        self.assertIsNone(partial["available"])
        self.assertEqual((partial["commitments"], partial["allocation"][0]["amount"]), ("30.00", "12.00"))
        self.assertTrue(partial["warnings"])
        update_profile(self.db, {"commitments": "100"})
        self.assertEqual(self.summary(bank=bank)["contribution"]["available"], "380.00")
        bank["balance_fresh"] = False
        stale = self.summary(bank=bank)["contribution"]
        self.assertIsNone(stale["available"])
        self.assertEqual(stale["balance"], "500.00")
        self.assertTrue(stale["warnings"])
        update_profile(self.db, {"balance": "400"})
        override = self.summary(bank=bank)["contribution"]
        self.assertEqual(override["available"], "280.00")
        self.assertFalse(override["warnings"])

    def test_profile_allocation_and_cent_rounding(self):
        self.assertFalse(self.summary()["profile"]["targets"])
        update_profile(self.db, {"goal": "Reserva", "risk": "moderate", "horizon_months": 36, "reserve": "50.20"})
        self.assertEqual(self.summary()["profile"]["targets"]["Ações"], "20.00")
        update_profile(self.db, {"targets": {"A": "10", "B": "45", "C": "45"}})
        upsert_position(self.db, {"name": "A1", "asset_class": "A", "value": "100.00"})
        parts = self.summary(amount="2.01")["contribution"]["allocation"]
        self.assertEqual([part["amount"] for part in parts], ["0.00", "1.01", "1.00"])
        self.assertEqual(sum(Decimal(part["projected_value"]) for part in parts), Decimal("102.01"))
        self.assertEqual(allocate({}, {"A": Decimal(100)}, Decimal(0))[0]["amount"], "0.00")
        update_profile(self.db, {"targets": None, "reserve": 0})
        self.assertEqual(self.summary()["profile"]["targets"], {"Renda fixa": "100.00"})
        with self.assertRaisesRegex(ValueError, "100%"):
            update_profile(self.db, {"targets": {"A": "99.99"}})

    def test_calculators_and_bounds(self):
        common = {"principal": "100.00", "annual_rate": "0", "monthly": "10.00", "months": 3, "target": "130.01"}
        for mode, value in [("compound", "130.00"), ("monthly", "10.01"), ("time", 4)]:
            self.assertEqual(calculate(dict(common, mode=mode))["value"], value)
        compound = calculate(dict(common, mode="compound", annual_rate="12", monthly=0, months=12))
        self.assertEqual(compound["value"], "112.00")
        annual = dict(common, annual_rate="10", monthly="0", target="110", months=12)
        self.assertEqual(calculate(dict(annual, mode="time"))["value"], 12)
        self.assertEqual(calculate(dict(annual, mode="monthly"))["value"], "0.00")
        with self.assertRaisesRegex(ValueError, "atingida"):
            calculate(dict(common, mode="time", monthly=0))
        for value in ("NaN", "Infinity", -1, "9000000000000", None, "20.999", True):
            with self.subTest(value=value), self.assertRaises(ValueError):
                money(value)
        self.assertIsNone(self.db.get(Profile, 1))

    def test_monthly_rate_uses_compounding_in_all_calculators(self):
        data = {"principal": "100.00", "monthly_rate": "1,00", "monthly": "10.00",
                "months": 3, "target": "133.34"}
        projection = calculate(dict(data, mode="compound"))
        self.assertEqual((projection["value"], projection["invested"], projection["earnings"]),
                         ("133.33", "130.00", "3.33"))
        self.assertAlmostEqual(Decimal(projection["equivalent_rate"]), Decimal("12.68250301"))
        self.assertEqual(calculate(dict(data, mode="time"))["value"], 4)
        self.assertEqual(calculate(dict(data, mode="monthly"))["value"], "10.01")

        annual = {key: value for key, value in data.items() if key != "monthly_rate"}
        projection = calculate(dict(annual, mode="compound", annual_rate="12.68250301"))
        self.assertEqual(projection["value"], "133.33")
        self.assertAlmostEqual(Decimal(projection["equivalent_rate"]), Decimal("1"))

        with self.assertRaisesRegex(ValueError, "uma única taxa"):
            calculate(dict(data, mode="compound", annual_rate="12"))
        with self.assertRaisesRegex(ValueError, "taxa mensal"):
            calculate(dict(data, mode="compound", monthly_rate="-100"))

    def test_json_api_rolls_back_an_invalid_update(self):
        @contextmanager
        def session_scope():
            with Session(self.engine) as db, db.begin():
                yield db

        app = Flask(__name__)
        app.register_blueprint(bp)
        with patch("openchris.finance_api.db_session", session_scope), app.test_client() as client:
            self.assertEqual(client.post("/api/profile", json={"balance": "100", "minimum": "5",
                                                               "commitments": "12.50"}).status_code, 200)
            bad = client.post("/api/profile", json={"balance": "500", "targets": {"A": "110"}})
            self.assertEqual(bad.status_code, 400)
            self.assertIn("100%", bad.json["error"])
            view = client.get("/api/finance?month=2025-02&amount=1.21")
            self.assertEqual(view.json["contribution"]["available"], "82.50")
            self.assertEqual(view.json["contribution"]["simulated"], "1.21")
            self.assertEqual(client.get("/api/finance?month=2025-13").status_code, 400)
            self.assertEqual(client.post("/api/positions", json=[1]).status_code, 400)


if __name__ == "__main__":
    unittest.main()
