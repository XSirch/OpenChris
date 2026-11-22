"""Saldos observados, movimentos de investimento e referências em reais."""

import base64
import json
from collections import defaultdict
from datetime import date, datetime, timedelta, timezone
from decimal import Decimal, InvalidOperation
from zoneinfo import ZoneInfo

import requests
from sqlalchemy import Date, DateTime, JSON, String, false, select
from sqlalchemy.orm import Mapped, mapped_column

from .db import Base, db_session
from .finance import ZERO, _cash, today_local

CDI_URL = "https://api.bcb.gov.br/dados/serie/bcdata.sgs.12/dados"
IPCA_URL = "https://apisidra.ibge.gov.br/values/t/1737/n1/1/v/2266/p/"
B3_URL = "https://sistemaswebb3-listados.b3.com.br/indexStatisticsProxy/IndexCall/GetPortfolioDay/"
LOCAL = ZoneInfo("America/Sao_Paulo")
SOURCES = {
    "cdi": CDI_URL + "?formato=json",
    "ipca": "https://www.ibge.gov.br/estatisticas/economicas/precos-e-custos/9256-ipca.html",
    "ibov": "https://sistemaswebb3-listados.b3.com.br/indexStatisticsPage/daily-evolution/IBOV?language=pt-br",
}


class InvestmentBalance(Base):
    __tablename__ = "investment_balances"
    connection_id: Mapped[str] = mapped_column(String(36), primary_key=True)
    day: Mapped[date] = mapped_column(Date, primary_key=True)
    name: Mapped[str]
    valued_on: Mapped[date | None] = mapped_column(Date)
    estimated: Mapped[bool] = mapped_column(default=False, server_default=false())
    positions: Mapped[dict] = mapped_column(JSON)
    flow_start: Mapped[date | None] = mapped_column(Date)
    movements: Mapped[list] = mapped_column(JSON, default=list)


class MarketSeries(Base):
    __tablename__ = "performance_market_series"
    code: Mapped[str] = mapped_column(String(8), primary_key=True)
    values: Mapped[dict] = mapped_column(JSON, default=dict)
    checked_at: Mapped[datetime] = mapped_column(DateTime)
    fetched_at: Mapped[datetime | None] = mapped_column(DateTime)


def _day(raw):
    return date.fromisoformat(raw[:10])


def _number(raw):
    value = Decimal(str(raw))
    if not value.is_finite():
        raise ValueError("Valor não finito.")
    return value


def _investment_flows(positions, previous, start, end, read):
    movements = {}
    for item_id in sorted(positions.keys() | previous.keys()):
        history = list(read(f"/investments/{item_id}/transactions", {"pageSize": 100}))
        anchored = False
        received = False
        exited = False
        for raw in history:
            trade = raw.get("tradeDate")
            booked = _day(trade or raw["date"])
            if booked <= start:
                anchored |= bool(trade)
                continue
            if booked > end:
                continue
            if not trade or raw["type"] not in ("BUY", "SELL", "AMORTIZATION", "INTEREST", "TRANSFER", "TAX"):
                raise ValueError("Movimentação de investimento sem conciliação.")
            kind = raw["type"]
            direction = raw.get("movementType")
            if kind == "TRANSFER" and direction not in ("CREDIT", "DEBIT"):
                raise ValueError("Transferência de investimento sem direção.")
            sign = 1 if kind == "BUY" or (kind == "TRANSFER" and direction == "CREDIT") else -1
            net = raw.get("netAmount")
            amount = abs(_number(raw["amount"] if net is None else net))
            movement = {"id": str(raw["id"]), "day": booked.isoformat(), "kind": kind, "amount": str(sign * amount)}
            if movement["id"] in movements and movements[movement["id"]] != movement:
                raise ValueError("Movimentações divergentes para o mesmo identificador.")
            movements[movement["id"]] = movement
            received |= kind in ("BUY", "TRANSFER") and sign == 1
            exited |= kind in ("SELL", "TRANSFER", "AMORTIZATION") and sign == -1
        if ((item_id in previous and not anchored) or (item_id not in previous and not received)
                or (item_id not in positions and not exited)):
            raise ValueError("Sem histórico de movimentações suficiente.")
    return list(movements.values())


def _snapshot_day(raw):
    stamp = datetime.fromisoformat(raw)
    return (stamp if stamp.tzinfo else stamp.replace(tzinfo=timezone.utc)).astimezone(LOCAL).date()


def investment_observation(item_id, raw_rows, read, today=None, source_updated_at=None):
    """Read movements only when a new common valuation date is available."""
    today = today or today_local()
    brl = [row for row in raw_rows if row.get("currencyCode") == "BRL"]
    if not brl:
        return None
    active = [row for row in brl if row.get("status") in ("ACTIVE", None)]
    if not active and not all(row.get("status") == "TOTAL_WITHDRAWAL" for row in brl):
        return None
    positions = {str(row["id"]): str(_number(row["balance"])) for row in active}
    try:
        references = {_day(row["date"]) if row.get("date") else None for row in active or brl}
    except (ValueError, TypeError):
        references = set()
    first_fresh = today - timedelta(days=4)
    valued_on = next(iter(references)) if len(references) == 1 else None
    estimated = False
    if not valued_on or not first_fresh <= valued_on <= today:
        valued_on = None
        try:
            reported_on = _snapshot_day(source_updated_at)
            updates = [_snapshot_day(row["updatedAt"]) for row in active or brl]
            if first_fresh <= reported_on <= today and all(
                first_fresh <= updated <= reported_on for updated in updates
            ):
                valued_on, estimated = reported_on, True
        except (ValueError, TypeError, KeyError):
            pass
    with db_session() as db:
        current = db.get(InvestmentBalance, (item_id, today))
        if current and current.positions == positions and current.valued_on == valued_on:
            return None
        previous = db.scalar(select(InvestmentBalance).where(
            InvestmentBalance.connection_id == item_id, InvestmentBalance.valued_on < valued_on,
        ).order_by(InvestmentBalance.valued_on.desc(), InvestmentBalance.day.desc()).limit(1)) if valued_on else None
    flow_start, movements = None, []
    if previous:
        try:
            movements = _investment_flows(positions, previous.positions, previous.valued_on, valued_on, read)
            flow_start = previous.valued_on
        except (requests.RequestException, ValueError, InvalidOperation, KeyError, TypeError):
            pass
    return dict(connection_id=item_id, day=today, valued_on=valued_on, estimated=estimated, positions=positions,
                flow_start=flow_start, movements=movements)


def _get_json(url, **params):
    response = requests.get(url, params=params, timeout=8)
    response.raise_for_status()
    return response.json()


def _read_cdi(first, last):
    rows = _get_json(CDI_URL, formato="json", dataInicial=first.strftime("%d/%m/%Y"),
                     dataFinal=last.strftime("%d/%m/%Y"))
    return {datetime.strptime(row["data"], "%d/%m/%Y").date().isoformat(): str(_number(row["valor"]))
            for row in rows}


def _read_ipca(first, last):
    months = (last.year - first.year) * 12 + last.month - first.month + 1
    data = _get_json(IPCA_URL + f"last%20{months}")
    return {row["D3C"][:4] + "-" + row["D3C"][4:]: str(_number(row["V"]))
            for row in data[1:] if row["V"] not in ("...", "-", "X")}


def _read_ibov(first, last):
    values = {}
    for year in range(first.year, last.year + 1):
        params = base64.b64encode(json.dumps({"index": "IBOVESPA", "language": "pt-br", "year": year}).encode()).decode()
        for row in _get_json(B3_URL + params)["results"]:
            for month in range(1, 13):
                value = row.get(f"rateValue{month}")
                if not value or not value[0].isdigit():
                    continue
                day = date(year, month, int(row["day"]))
                if first <= day <= last:
                    values[day.isoformat()] = str(_number(value.replace(".", "").replace(",", ".")))
    return values


def refresh_benchmarks():
    now = datetime.now(timezone.utc).replace(tzinfo=None)
    today = today_local()
    first = date(today.year - 1, 12, 1)
    for code, read in (("cdi", _read_cdi), ("ipca", _read_ipca), ("ibov", _read_ibov)):
        with db_session() as db:
            record = db.get(MarketSeries, code)
            wait = timedelta(hours=6) if record and record.fetched_at == record.checked_at else timedelta(minutes=30)
            if record and now - record.checked_at < wait:
                continue
        try:
            values = read(first, today)
        except (requests.RequestException, ValueError, InvalidOperation, KeyError, TypeError):
            values = {}
        with db_session() as db:
            record = db.get(MarketSeries, code) or MarketSeries(code=code, values={})
            record.checked_at = now
            if values:
                record.values = {**record.values, **values}
                record.fetched_at = now
            db.add(record)


def _balances(rows):
    # The same position can arrive through two connections.
    positions = {}
    for row in rows.values():
        for key, value in row.positions.items():
            amount = _number(value)
            if key in positions and positions[key] != amount:
                return None
            positions[key] = amount
    return sum(positions.values(), ZERO)


def _dietz(start, end, old, new):
    initial, final = _balances(old), _balances(new)
    if initial is None or final is None or not initial or old.keys() != new.keys():
        return None
    if any(row.flow_start != start for row in new.values()):
        return None
    moves = {}
    for row in new.values():
        for movement in row.movements:
            key = movement["id"]
            if key in moves and moves[key] != movement:
                return None
            moves[key] = movement
    flow, income, weighted = ZERO, ZERO, ZERO
    transfers = defaultdict(Decimal)
    for movement in moves.values():
        day, kind, amount = _day(movement["day"]), movement["kind"], _number(movement["amount"])
        if not start < day <= end:
            return None
        if kind == "INTEREST":
            income -= amount
        elif kind == "TRANSFER":
            transfers[day] += amount
        elif kind != "TAX":
            flow += amount
            weighted += amount * Decimal((end - day).days) / (end - start).days
    if any(transfers.values()) or initial + weighted <= 0:
        return None
    return (final - initial - flow + income) / (initial + weighted), flow, income


def _month_end(day):
    return (day.replace(day=28) + timedelta(days=4)).replace(day=1) - timedelta(days=1)


def _reference(code, values, start, end):
    if code == "ipca":
        first = start.strftime("%Y-%m")
        last = max((month for month in values if month < end.strftime("%Y-%m")
                    or month == end.strftime("%Y-%m") and end == _month_end(end)), default="")
        if first not in values or not last or last <= first:
            return None
        base, through = _month_end(start), _month_end(_day(last + "-01"))
        return base, through, _number(values[last]) / _number(values[first]) - 1
    first = max((day for day in values if day <= start.isoformat()), default="")
    last = max((day for day in values if day <= end.isoformat()), default="")
    if not first or last <= first:
        return None
    if code == "ibov":
        rate = _number(values[last]) / _number(values[first]) - 1
    else:
        rate = Decimal(1)
        for day, value in values.items():
            if first < day <= last:
                rate *= 1 + _number(value) / 100
        rate -= 1
    return start, _day(last), rate


def portfolio_performance(db, today=None):
    from .banking import Connection
    from .finance import Position

    today = today or today_local()
    year_start = date(today.year - 1, 12, 31)
    rows = list(db.scalars(select(InvestmentBalance).where(InvestmentBalance.day >= year_start)
                           .order_by(InvestmentBalance.day, InvestmentBalance.connection_id)))
    observations, valuations = defaultdict(dict), defaultdict(dict)
    connections = {row.id: row for row in db.scalars(select(Connection))}
    # Carry no missing connection forward. Re-observing an old quote doesn't create a return point.
    for row in rows:
        if row.connection_id in connections:
            observations[row.day][row.connection_id] = row
            if row.valued_on and row.valued_on >= year_start:
                previous = valuations[row.valued_on].get(row.connection_id)
                if not previous or row.flow_start or not previous.flow_start:
                    valuations[row.valued_on][row.connection_id] = row

    dates = sorted(valuations)
    linked, flows, income = Decimal(1), ZERO, ZERO
    chain = {}
    for start, end in zip(dates, dates[1:]):
        result = _dietz(start, end, valuations[start], valuations[end])
        if result is None:
            chain, linked, flows, income = {}, Decimal(1), ZERO, ZERO
            continue
        if not chain:
            chain[start] = ZERO
        rate, flow, dividend = result
        linked *= 1 + rate
        flows += flow
        income += dividend
        chain[end] = linked - 1
    base = next(iter(chain), None)
    end = next(reversed(chain), None)
    scope = valuations[end] if end else observations[max(observations)] if observations else {}
    change = _balances(scope) - _balances(valuations[base]) if base else None
    manual = db.scalar(select(Position.id).where(Position.active.is_(True), Position.currency == "BRL",
                                                 Position.source != "pluggy").limit(1)) is not None
    partial = bool(connections.keys() - scope.keys()) or manual or any(not row.valued_on for row in scope.values())
    observed_scope = [{"date": day, "value": _cash(_balances({key: group[key] for key in scope}))}
                      for day, group in observations.items() if scope.keys() <= group.keys()]
    data = {
        "year": today.year, "start": base, "end": end, "partial": partial,
        "estimated": any(row.estimated for day in chain for row in valuations[day].values()),
        "stale": bool(end and (max(observations) > end + timedelta(days=1)
                               or any(connections[key].status != "Atualizada" for key in scope))),
        "institutions": sorted({connections[key].name or "Instituição não identificada" for key in scope}),
        "observations": observed_scope,
        "return_points": [{"date": day, "percent": _cash(rate * 100)} for day, rate in chain.items()],
        "net_flows": _cash(flows) if end else None, "income": _cash(income) if end else None,
        "balance_change": _cash(change), "investment_result": _cash(change - flows + income) if base else None,
        "benchmarks": {},
    }
    for series in db.scalars(select(MarketSeries)):
        reference = _reference(series.code, series.values, base or year_start, end or today)
        if not reference:
            continue
        first, last, rate = reference
        # Comparison uses the portfolio return over exactly the benchmark's dates.
        wallet = ((1 + chain[last]) / (1 + chain[first]) - 1
                  if first in chain and last in chain and chain[first] > -1 else None)
        points = []
        for day in chain or (year_start, last):
            point = _reference(series.code, series.values, first, day)
            if point and point[1] == day:
                points.append({"date": day, "percent": _cash(point[2] * 100)})
        data["benchmarks"][series.code] = dict(
            source=SOURCES[series.code], start=first, end=last, percent=_cash(rate * 100),
            difference=_cash((wallet - rate) * 100) if wallet is not None else None,
            delayed=series.fetched_at != series.checked_at,
            points=[{"date": first, "percent": "0.00"}, *points],
        )
    # Flask's default encoding of dates is RFC 1123. The UI uses ISO dates.
    return _iso_dates(data)


def _iso_dates(value):
    if isinstance(value, date):
        return value.isoformat()
    if isinstance(value, list):
        return [_iso_dates(item) for item in value]
    if isinstance(value, dict):
        return {key: _iso_dates(item) for key, item in value.items()}
    return value
