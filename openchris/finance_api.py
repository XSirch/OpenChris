from flask import Blueprint, jsonify, request
from sqlalchemy.exc import IntegrityError

from .banking import bank_snapshot
from .db import db_session
from .finance import (advance_installments, calculate, correct_entry, dashboard, entry_data,
                      position_data, update_profile, upsert_entry, upsert_position)

bp = Blueprint("finance", __name__)


@bp.errorhandler(ValueError)
def invalid_input(error):
    return jsonify(error=str(error)), 400


@bp.errorhandler(IntegrityError)
def conflicting_update(_error):
    return jsonify(error="Outro registro foi atualizado ao mesmo tempo. Atualize a página e tente novamente."), 409


def _payload():
    data = request.get_json(silent=True)
    if not isinstance(data, dict) or not data:
        raise ValueError("Envie os campos do formulário.")
    return data


@bp.get("/api/finance")
def summary():
    with db_session() as db:
        return jsonify(dashboard(db, request.args.get("month"), request.args.get("amount"), bank=bank_snapshot(db)))


@bp.post("/api/positions")
def positions():
    with db_session() as db:
        return jsonify(position_data(upsert_position(db, _payload())))


@bp.post("/api/entries")
def entries():
    with db_session() as db:
        return jsonify(entry_data(upsert_entry(db, _payload())))


@bp.post("/api/profile")
def profile():
    with db_session() as db:
        update_profile(db, _payload())
        return jsonify(status="saved")


@bp.post("/api/corrections")
def corrections():
    with db_session() as db:
        return jsonify(entry_data(correct_entry(db, _payload())))


@bp.post("/api/advance")
def advance():
    with db_session() as db:
        return jsonify(entry_data(advance_installments(db, _payload())))


@bp.post("/api/calculate")
def calculator():
    return jsonify(calculate(_payload()))
