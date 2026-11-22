from datetime import date, datetime, timedelta
from decimal import Decimal

import pytest

from openchris.banking import Connection
from openchris.db import db_session, initialize
from openchris.performance import (InvestmentBalance, MarketSeries, _investment_flows,
                                   _reference, investment_observation, portfolio_performance)


@pytest.fixture
def database(tmp_path, monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "sqlite:///" + str(tmp_path / "performance.db"))
    initialize()


def transaction(key, kind, day, amount, **kwargs):
    return {"id": key, "type": kind, "date": day, "tradeDate": day, "amount": amount, **kwargs}


def test_observations_compound_without_counting_new_cash_as_gain(database):
    start = date(2026, 1, 6)
    baseline = [{"id": "bond", "currencyCode": "BRL", "date": start.isoformat(), "balance": "1000"}]
    history = [transaction("initial", "BUY", "2025-12-12", "1000"),
               transaction("deposit", "BUY", "2026-01-07", "500")]

    def read(path, query):
        assert path == "/investments/bond/transactions"
        return history

    with db_session() as db:
        db.add(Connection(id="bank", name="Banco A", status="Atualizada"))
        db.add(Connection(id="missing", name="Banco B"))

    def save(day, balance):
        rows = [{**baseline[0], "date": day.isoformat(), "balance": balance}]
        observed = investment_observation("bank", rows, read, day)
        with db_session() as db:
            db.merge(InvestmentBalance(name="Banco A", **observed))

    save(start, "1000")
    save(start + timedelta(days=1), "1500")
    with db_session() as db:
        view = portfolio_performance(db, start + timedelta(days=1))
        assert view["return_points"][-1]["percent"] == "0.00"
        assert view["net_flows"] == "500.00" and view["partial"]
        assert view["institutions"] == ["Banco A"]

    history.extend([transaction("sale", "SELL", "2026-01-08", "100"),
                    transaction("div", "INTEREST", "2026-01-08", "30")])
    save(start + timedelta(days=2), "1400")
    with db_session() as db:
        stamp = datetime(2026, 1, 8)
        db.add(MarketSeries(code="cdi", values={"2026-01-06": "0.05", "2026-01-07": "0.05",
                                               "2026-01-08": "0.05"}, checked_at=stamp, fetched_at=stamp))
        db.flush()
        view = portfolio_performance(db, start + timedelta(days=2))
        assert view["return_points"][-1]["percent"] == "2.00"
        assert view["net_flows"] == "400.00" and view["income"] == "30.00"
        assert view["benchmarks"]["cdi"]["difference"] == "1.90"

    history.clear()
    save(start + timedelta(days=3), "1600")
    with db_session() as db:
        view = portfolio_performance(db, start + timedelta(days=3))
        assert not view["return_points"]
        assert view["observations"][-1]["value"] == "1600.00"


def test_movements_must_explain_an_asset_entering_or_leaving():
    with pytest.raises(ValueError):
        _investment_flows({}, {"asset": "100"}, date(2026, 1, 10), date(2026, 1, 11),
                          lambda *_: [transaction("buy", "BUY", "2026-01-01", "100")])


def test_ipca_only_uses_published_complete_months():
    values = {"2025-12": "100", "2026-01": "102", "2026-02": "105"}
    assert _reference("ipca", values, date(2025, 12, 31), date(2026, 2, 18)) == (
        date(2025, 12, 31), date(2026, 1, 31), Decimal("0.02"),
    )
    assert _reference("ipca", values, date(2026, 1, 15), date(2026, 2, 18)) is None
