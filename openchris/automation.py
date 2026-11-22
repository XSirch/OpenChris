"""Persistent background jobs for the single worker. Network calls run outside DB transactions."""

import hashlib
import json
import os
import re
import subprocess
from datetime import date, datetime, timedelta, timezone
from urllib.parse import urlsplit
from uuid import uuid4
from zoneinfo import ZoneInfo

import requests
from flask import Blueprint, jsonify, request
from sqlalchemy import JSON, Date, DateTime, String, func, or_, select, update
from sqlalchemy.orm import Mapped, mapped_column

from . import finance
from .agent_runner import (AgentPause, ModelUnavailable, OFFICIAL_DOMAINS, SCHEMAS, codex_result,
                           model_catalog, normalized, source_hosts, validate_result, verify_fields)
from .db import Base, db_session
from .economy import FEEDS

bp = Blueprint("automation", __name__)
LOCAL = ZoneInfo("America/Sao_Paulo")
LEGACY_DAILY_LIMIT_NOTICE = "O limite diário de execuções foi atingido. A fila retoma à meia-noite de Brasília."
AGENTS = {"expense": "Classificação de gastos", "classify": "Classificação de investimentos",
          "asset": "Pesquisa de ativos", "macro": "Economia", "risk": "Risco da carteira",
          "report": "Síntese dos especialistas", "chat": "Chat financeiro"}
INITIAL_AGENT_SETTINGS = {
    "chat": {"model": "gpt-6.1-sol", "effort": "medium"},
    "expense": {"model": "gpt-6-luna", "effort": "low"},
    "classify": {"model": "gpt-6-luna", "effort": "medium"},
    "asset": {"model": "gpt-6.1-sol", "effort": "medium"},
    "macro": {"model": "gpt-6.1-sol", "effort": "medium"},
    "risk": {"model": "gpt-6-astra", "effort": "high"},
    "report": {"model": "gpt-6.1-sol", "effort": "medium"},
}


def model_notice(kind):
    return f"{AGENTS[kind]}: revise o modelo e o esforço em Agentes e pesquisas."


def now():
    return datetime.now(timezone.utc).replace(tzinfo=None)


def day():
    return now().replace(tzinfo=timezone.utc).astimezone(LOCAL).date()


def iso(value):
    return value.isoformat(timespec="seconds") + "Z" if value else None


class Job(Base):
    __tablename__ = "agent_jobs"
    id: Mapped[int] = mapped_column(primary_key=True)
    key: Mapped[str] = mapped_column(String(150), unique=True)
    kind: Mapped[str]
    payload: Mapped[dict] = mapped_column(JSON)
    state: Mapped[str] = mapped_column(default="pending", index=True)
    priority: Mapped[int]
    attempts: Mapped[int] = mapped_column(default=0)
    ready_at: Mapped[datetime] = mapped_column(DateTime, default=now)
    finished_at: Mapped[datetime | None] = mapped_column(DateTime)
    notice: Mapped[str] = mapped_column(default="")


class Runtime(Base):
    __tablename__ = "agent_runtime"
    key: Mapped[str] = mapped_column(String(80), primary_key=True)
    checked_at: Mapped[datetime | None] = mapped_column(DateTime)
    until: Mapped[datetime | None] = mapped_column(DateTime)
    count: Mapped[int] = mapped_column(default=0)
    notice: Mapped[str] = mapped_column(default="")


class CodexSettings(Base):
    __tablename__ = "codex_settings"
    id: Mapped[int] = mapped_column(primary_key=True)
    models: Mapped[dict] = mapped_column(JSON, default=dict)
    preferences: Mapped[dict] = mapped_column(JSON, default=dict)
    refresh_after: Mapped[datetime] = mapped_column(DateTime)


def refresh_models(*, force=False):
    with db_session() as db:
        settings = db.get(CodexSettings, 1)
        if not force and settings and settings.refresh_after > now():
            return
    try:
        models = model_catalog()
    except (OSError, subprocess.SubprocessError, KeyError, TypeError, ValueError):
        models = None
    with db_session() as db:
        settings = db.get(CodexSettings, 1)
        if settings is None:
            settings = CodexSettings(id=1)
            db.add(settings)
        if models:
            if not settings.models:
                initial = {kind: option for kind, option in INITIAL_AGENT_SETTINGS.items()
                           if accepts(models, **option)}
                settings.preferences = {**initial, **(settings.preferences or {})}
            settings.models = models
        settings.refresh_after = now() + timedelta(minutes=60 if models else 5)


def accepts(models, model, effort):
    if not model:
        return not effort
    return model in models and (not effort or effort in models[model]["efforts"])


class AssetFile(Base):
    __tablename__ = "asset_files"
    identifier: Mapped[str] = mapped_column(String(40), primary_key=True)
    status: Mapped[str]
    fields: Mapped[list] = mapped_column(JSON)
    note: Mapped[str]
    checked_at: Mapped[date] = mapped_column(Date)


class Event(Base):
    __tablename__ = "economic_events"
    id: Mapped[int] = mapped_column(primary_key=True)
    key: Mapped[str] = mapped_column(String(80), unique=True)
    provider: Mapped[str]
    date: Mapped[date] = mapped_column(Date, index=True)
    title: Mapped[str]
    source_url: Mapped[str]
    fact: Mapped[str | None]
    analysis: Mapped[dict | None] = mapped_column(JSON)
    previous_analysis: Mapped[dict | None] = mapped_column(JSON)
    updated_at: Mapped[datetime] = mapped_column(DateTime, default=now)


def runtime(db, key):
    record = db.get(Runtime, key)
    if not record:
        record = Runtime(key=key, notice="", count=0)
        db.add(record)
    return record


def digest(value):
    return hashlib.sha256(json.dumps(value, ensure_ascii=True, sort_keys=True).encode()).hexdigest()[:32]


def enqueue(db, kind, version, payload, priority):
    key = kind + ":" + digest(version)
    if not db.scalar(select(Job.id).where(Job.key == key)):
        db.add(Job(key=key, kind=kind, payload=payload, priority=priority))


def refresh_feeds():
    for name, (_url, collect) in FEEDS.items():
        with db_session() as db:
            status = runtime(db, name)
            if status.until and status.until > now():
                continue
        try:
            events = collect(day())
            with db_session() as db:
                for key, due, title, fact, url in events:
                    event = db.scalar(select(Event).where(Event.key == key))
                    if not event:
                        event = Event(key=key, provider=name)
                        db.add(event)
                    if event.fact and not fact:
                        continue
                    if (event.date, event.title, event.fact, event.source_url) != (due, title, fact, url):
                        if event.analysis:
                            event.previous_analysis = event.analysis
                        event.date, event.title, event.fact, event.source_url = due, title, fact, url
                        event.analysis = None
                        event.updated_at = now()
                record = runtime(db, name)
                record.checked_at, record.until, record.notice = now(), now() + timedelta(hours=3), ""
        except (requests.RequestException, ValueError, TypeError, IndexError, KeyError):
            with db_session() as db:
                record = runtime(db, name)
                record.until = now() + timedelta(minutes=15)
                record.notice = "A fonte está indisponível. Uma nova consulta já está agendada."


def phase(event):
    if event.fact:
        return "published"
    return "scheduled" if event.date >= day() else "awaiting"


def event_data(event):
    return dict(id=event.id, date=event.date.isoformat(), title=event.title, phase=phase(event), fact=event.fact,
                source_url=event.source_url, analysis=event.analysis, previous_analysis=event.previous_analysis,
                updated_at=iso(event.updated_at))


def signature(event):
    return digest([event.date.isoformat(), phase(event), event.fact, event.source_url])


def public_identifier(position):
    raw = position.identifier.strip().upper()
    if ("fundo" in normalized(position.asset_class + " " + position.name)
            and re.fullmatch(r"\d{2}\.\d{3}\.\d{3}/\d{4}-\d{2}", raw)):
        return raw
    if (re.fullmatch(r"[A-Z]{1,5}(?:[.-][A-Z]{1,2})?", raw)
            and normalized(position.asset_class) in ("ações", "exterior", "etfs", "stocks", "bdrs")):
        return raw
    if re.fullmatch(r"[A-Z][A-Z0-9.\-]{2,23}", raw) and re.search(r"[0-9]", raw):
        return raw
    if re.fullmatch(r"\d[A-Z0-9]{7}\d", raw) and re.search(r"[A-Z]", raw):
        return raw
    return ""


def expense_input(entry):
    def redact(value):
        return re.sub(r"[\w.+-]+@[\w.-]+|(?<!\w)\+?\d[\d .()/\-]{6,}\d", "[oculto]", value)[:160]

    return dict(id=entry.id, description=redact(entry.original_description), merchant=redact(entry.merchant),
                kind=entry.kind, bank_direction=entry.bank_direction, card=entry.card,
                installment=entry.installment_index is not None)


def apply_file(db, position, file):
    compatible = True
    for field in file.fields:
        if not finance.apply_position_facts(db, position.id, {field["field"]: field["value"]}, field["source_url"]):
            compatible = False
    if not compatible or file.status in ("partial", "unavailable"):
        position.needs_review = True


def plan(db):
    for entry in db.scalars(select(finance.Entry).where(finance.Entry.active.is_(True), finance.Entry.classification_source == "pending",
                                                        finance.Entry.advanced_by.is_(None))):
        data = expense_input(entry)
        enqueue(db, "expense", data, data, 25)
    for pos in db.scalars(select(finance.Position).where(finance.Position.active.is_(True))):
        identifier = public_identifier(pos)
        file = db.get(AssetFile, identifier) if identifier else None
        if file:
            apply_file(db, pos, file)
        if identifier and (not file or file.checked_at <= day() - timedelta(days=30)):
            version = [identifier, file.checked_at.isoformat() if file else None]
            enqueue(db, "asset", version, {"identifier": identifier}, 30)
        if pos.asset_class == finance.UNKNOWN_CLASS:
            data = dict(id=pos.id, name=pos.name[:180], issuer=pos.issuer[:140])
            enqueue(db, "classify", data, data, 32)
    events = db.scalars(select(Event).where(Event.date.between(day() - timedelta(days=8), day() + timedelta(days=14))))
    for event in events:
        if phase(event) == "awaiting" or event.analysis:
            continue
        version = dict(id=event.id, signature=signature(event))
        distance = abs((event.date - day()).days)
        enqueue(db, "macro", version, version, 19 + min(distance, 20))


def report_context(db, event):
    from .banking import bank_snapshot

    dashboard = finance.dashboard(db, today=day(), bank=bank_snapshot(db))
    portfolio = dashboard["portfolio"]
    spending, budget = dashboard["spending"], dashboard["contribution"]
    assets = []
    for pos in db.scalars(select(finance.Position).where(finance.Position.active.is_(True))
                          .order_by(finance.Position.value.desc()).limit(24)):
        card = db.get(AssetFile, public_identifier(pos))
        assets.append({"classe": pos.asset_class, "indexador": pos.indexer, "valor": str(pos.value),
                       "moeda": pos.currency, "identificador": public_identifier(pos), "ficha": card.fields if card else []})
    return {"data": day().isoformat(), "evento": {k: v for k, v in event_data(event).items()
            if k in ("date", "title", "phase", "fact", "source_url")}, "carteira": portfolio["classes"],
            "ativos": assets, "aporte": {k: budget[k] for k in ("available", "shortfall", "allocation")},
            "gastos": {k: spending[k] for k in ("total", "average", "change", "contribution_impact")}}


def finish(job, notice=""):
    job.state, job.finished_at, job.notice = "done", now(), notice


def next_jobs(db):
    stale = db.scalars(select(Job).where(Job.state == "running", Job.ready_at <= now()))
    for job in stale:
        if job.kind == "telegram":
            job.state, job.notice = "uncertain", "Confira no Telegram se o aviso chegou."
        else:
            job.state = "retry"
    stmt = select(Job).where(Job.state.in_(("pending", "retry")), Job.ready_at <= now()).order_by(Job.priority, Job.id)
    job = db.scalar(stmt.limit(1))
    if not job:
        return []
    if job.kind == "expense":
        return list(db.scalars(stmt.where(Job.kind == "expense").limit(20)))
    return [job]


def valid_event(db, payload):
    event = db.get(Event, payload["id"])
    return event if event and signature(event) == payload["signature"] else None


def prepare(db, jobs):
    job, data = jobs[0], jobs[0].payload
    if job.kind == "chat":
        from .banking import bank_snapshot

        bank = bank_snapshot(db)
        view = finance.dashboard(db, month=data["month"], today=day(), bank=bank)
        spending = view["spending"]
        return {"pergunta": data["question"], "conversa_anterior": data["previous"],
                "mes": view["month"], "dados_ate": spending["cutoff"],
                "receitas_classificadas": str(sum(finance.money(e["amount"]) for e in spending["entries"]
                                                  if e["kind"] == "income")),
                "gastos": {k: spending[k] for k in ("total", "average", "history", "pending")},
                "categorias": [{k: category[k] for k in ("category", "total", "average")}
                               for category in spending["categories"][:50]],
                "carteira": view["portfolio"]["classes"],
                "aporte_atual": {k: view["contribution"][k] for k in
                                  ("balance", "available", "shortfall", "confirmed", "warnings", "allocation")}}
    if job.kind == "expense":
        current = []
        for row in jobs:
            entry = db.get(finance.Entry, row.payload["id"])
            if entry and entry.active and entry.classification_source == "pending" and expense_input(entry) == row.payload:
                current.append(row)
            else:
                finish(row)
        jobs[:] = current
        return [row.payload for row in jobs]
    if job.kind == "classify":
        pos = db.get(finance.Position, data["id"])
        if pos and pos.active and pos.asset_class == finance.UNKNOWN_CLASS and pos.name[:180] == data["name"]:
            return {"name": data["name"], "issuer": data["issuer"]}
    elif job.kind == "asset":
        active = db.scalars(select(finance.Position).where(finance.Position.active.is_(True)))
        if any(public_identifier(pos) == data["identifier"] for pos in active):
            return {"identificador": data["identifier"], "domínios": [*OFFICIAL_DOMAINS, "www.gov.br/cvm",
                    "www.gov.br/tesouronacional", *source_hosts()]}
    else:
        event = valid_event(db, data)
        if event and day() - timedelta(days=8) <= event.date <= day() + timedelta(days=14):
            if job.kind == "telegram":
                if telegram_enabled() and event.analysis and abs((event.date - day()).days) <= 7:
                    return {"id": event.id, "title": event.title, "conclusion": event.analysis["conclusion"]}
            elif job.kind == "macro":
                return report_context(db, event)
            else:
                return data["context"]
    finish(job)
    jobs.clear()
    return None


def telegram_enabled():
    base = urlsplit(os.getenv("PUBLIC_BASE_URL", ""))
    return (os.getenv("TELEGRAM_ENABLED") == "1" and bool(os.getenv("TELEGRAM_BOT_TOKEN"))
            and bool(os.getenv("TELEGRAM_CHAT_ID")) and base.scheme == "https" and bool(base.hostname))


def send_telegram(data):
    base = os.environ["PUBLIC_BASE_URL"].rstrip("/")
    message = f"{data['title']}\n\n{data['conclusion'][:900]}\n\n{base}/economia?evento={data['id']}"
    url = f"https://api.telegram.org/bot{os.environ['TELEGRAM_BOT_TOKEN']}/sendMessage"
    response = requests.post(url, json={"chat_id": os.environ["TELEGRAM_CHAT_ID"], "text": message,
                             "link_preview_options": {"is_disabled": True}}, timeout=(5, 15))
    if response.status_code == 429:
        raise AgentPause("O Telegram limitou os avisos. O worker tentará de novo.", 15)
    if response.status_code in (400, 401, 403, 404):
        raise AgentPause("Confira o token do bot e o chat configurado no Telegram.", 60)
    response.raise_for_status()
    if response.json().get("ok") is not True:
        raise ValueError("O Telegram não confirmou a entrega.")
    return {}


def save_result(db, jobs, context, out, verified):
    job, data = jobs[0], jobs[0].payload
    if job.kind == "chat":
        if not out["answer"].strip():
            raise ValueError("Resposta vazia.")
        job.payload = {"question": data["question"], "month": data["month"], "answer": out["answer"]}
    elif job.kind == "expense":
        answers = {item["id"]: item for item in out["items"]}
        if len(answers) != len(out["items"]) or set(answers) != {r.payload["id"] for r in jobs}:
            raise ValueError("O lote contém identificadores incompatíveis.")
        for row in jobs:
            item = answers[row.payload["id"]]
            entry = db.get(finance.Entry, row.payload["id"], with_for_update=True)
            if (entry and entry.active and expense_input(entry) == row.payload and entry.classification_source == "pending"
                    and item["confident"] and item["kind"] != "unknown" and 0 < len(item["category"].strip()) <= 100):
                finance.classify_entry(db, entry.id, item["kind"], item["category"])
    elif job.kind == "classify":
        pos = db.get(finance.Position, data["id"], with_for_update=True)
        if (pos and pos.active and pos.asset_class == finance.UNKNOWN_CLASS and pos.name[:180] == data["name"]
                and out["confident"] and 2 <= len(out["evidence"].strip()) <= 180
                and normalized(out["evidence"]) in normalized(data["name"]) and 0 < len(out["asset_class"]) <= 100):
            finance.apply_position_facts(db, pos.id, {"asset_class": out["asset_class"]},
                                         f"{pos.source} · descrição: {out['evidence']}")
    elif job.kind == "asset":
        identifier = data["identifier"]
        fields, conflicts = verified
        previous = db.get(AssetFile, identifier)
        current = {f["field"]: f for f in (previous.fields if previous else [])}
        for field in fields:
            old = current.get(field["field"])
            if old and normalized(old["value"]) != normalized(field["value"]):
                conflicts.add(field["field"])
            elif field["field"] not in conflicts:
                current[field["field"]] = field
        note = out["note"][:800]
        if conflicts:
            note = "Fontes divergem nos campos " + ", ".join(sorted(conflicts)) + ". " + note
        unverified = {f["field"] for f in out["fields"]} - {f["field"] for f in fields} - conflicts
        if unverified:
            labels = {"issuer": "emissor", "indexer": "indexador", "maturity": "vencimento", "asset_class": "classe"}
            missing = ", ".join(sorted(labels[k] for k in unverified))
            note = f"O documento não confirmou {missing}. " + note
        if not fields and not note.strip():
            note = ("A consulta mais recente não reconfirmou os campos da ficha." if current else
                    "Nenhum documento consultado confirmou os dados deste ativo.")
        status = "verified" if fields and not conflicts and not note.strip() else "partial" if current else "unavailable"
        file = db.merge(AssetFile(identifier=identifier, fields=list(current.values()), note=note, status=status,
                                  checked_at=day()))
        for pos in db.scalars(select(finance.Position).where(finance.Position.active.is_(True))):
            if public_identifier(pos) == identifier:
                apply_file(db, pos, file)
    elif job.kind in ("macro", "risk", "report"):
        event = valid_event(db, data)
        if event:
            version = {"id": event.id, "signature": signature(event)}
            if job.kind in ("macro", "risk"):
                urls = {event.source_url} | {f["source_url"] for pos in context["ativos"] for f in pos["ficha"]}
                if out["source_url"] not in urls or not out["argument"].strip() or not out["scenario"].strip():
                    raise ValueError("O argumento precisa citar uma das fontes consultadas.")
                role = "Economia" if job.kind == "macro" else "Risco da carteira"
                opinion = {**out, "specialist": role}
                opinions = [*context.get("argumentos", []), opinion]
                payload = {**version, "context": {**context, "argumentos": opinions}}
                enqueue(db, "risk" if job.kind == "macro" else "report", version, payload, 12)
            else:
                if not all(out[k].strip() for k in ("conclusion", "divergence", "impact")) or not out["scenarios"]:
                    raise ValueError("A conclusão da análise está incompleta.")
                event.analysis = {**out, "arguments": context["argumentos"], "phase": phase(event),
                                  "created_at": iso(now())}
                event.updated_at = now()
                if telegram_enabled():
                    enqueue(db, "telegram", version, version, 0)
    for row in jobs:
        finish(row)


def run_one():
    with db_session() as db:
        jobs = next_jobs(db)
        if not jobs:
            return False
        kind = jobs[0].kind
        context = prepare(db, jobs)
        if not jobs:
            return True
        if kind != "telegram":
            control = runtime(db, "codex")
            if control.notice == LEGACY_DAILY_LIMIT_NOTICE:
                control.notice, control.until = "", None
            if os.getenv("CODEX_ENABLED") != "1" or (control.until and control.until > now()):
                return False
            settings = db.get(CodexSettings, 1)
            options = settings.preferences.get(kind, {}) if settings else {}
            if options and not accepts(settings.models, **options):
                for job in jobs:
                    job.state, job.ready_at, job.notice = "retry", now() + timedelta(hours=1), model_notice(kind)
                return True
        for job in jobs:
            job.state, job.attempts, job.ready_at = "running", job.attempts + 1, now() + timedelta(minutes=15)
            job.notice = ""
        ids = [job.id for job in jobs]
    try:
        out = send_telegram(context) if kind == "telegram" else codex_result(kind, context, **options)
        verified = None
        if kind != "telegram":
            validate_result(out, SCHEMAS[kind])
        if kind == "asset":
            verified = verify_fields(context["identificador"], out["fields"], day())
        with db_session() as db:
            jobs = [db.get(Job, key) for key in ids]
            save_result(db, jobs, context, out, verified)
            if kind != "telegram":
                control = runtime(db, "codex")
                control.checked_at, control.notice, control.until = now(), "", None
    except Exception as error:
        with db_session() as db:
            for key in ids:
                job = db.get(Job, key)
                pause = isinstance(error, AgentPause)
                delay = error.minutes if pause else min(720, 5 * 2 ** min(job.attempts - 1, 8))
                job.ready_at = now() + timedelta(minutes=delay)
                job.state = "uncertain" if kind == "telegram" and not pause else "retry"
                job.notice = (str(error) if pause else "Confira no Telegram se o aviso chegou."
                              if kind == "telegram" else "A tarefa não terminou. Outra tentativa está agendada.")
                if isinstance(error, ModelUnavailable):
                    job.notice, job.ready_at = model_notice(kind), now() + timedelta(hours=1)
                if kind == "chat" and not pause:
                    job.state = "failed"
                    if not isinstance(error, ModelUnavailable):
                        job.notice = "A IA não concluiu a resposta. Envie a pergunta novamente."
                if pause and kind != "telegram":
                    control = runtime(db, "codex")
                    control.notice, control.until = job.notice, job.ready_at
        return not isinstance(error, AgentPause)
    return True


def tick():
    with db_session() as db:
        worker = runtime(db, "worker")
        worker.checked_at, worker.count = now(), int(os.getenv("CODEX_ENABLED") == "1")
    refresh_models()
    refresh_feeds()
    with db_session() as db:
        plan(db)
    for _ in range(8):
        if not run_one():
            break


def active_codex_notice(control):
    if control and control.until and control.until > now() and control.notice != LEGACY_DAILY_LIMIT_NOTICE:
        return control.notice
    return ""


@bp.route("/api/finance-chat", methods=["GET", "POST"])
def finance_chat():
    with db_session() as db:
        if request.method == "POST":
            data = request.get_json(silent=True)
            question = data.get("question") if isinstance(data, dict) else None
            month = data.get("month") if isinstance(data, dict) else None
            if not isinstance(question, str) or not 1 <= len(question.strip()) <= 1000:
                return jsonify(error="Digite uma pergunta de até 1.000 caracteres."), 400
            if not isinstance(month, str) or not re.fullmatch(r"20\d{2}-(0[1-9]|1[0-2])", month):
                return jsonify(error="Escolha um mês válido na aba Gastos."), 400
            if db.bind.dialect.name == "postgresql":
                db.execute(select(func.pg_advisory_xact_lock(73148612)))
            worker = db.get(Runtime, "worker")
            if not worker or not worker.count or not worker.checked_at or now() - worker.checked_at > timedelta(minutes=15):
                return jsonify(error="O worker da IA está indisponível no momento."), 503
            if db.scalar(select(Job.id).where(Job.kind == "chat", Job.state.in_(("pending", "running", "retry"))).limit(1)):
                return jsonify(error="Aguarde a resposta da pergunta anterior."), 409
            previous = list(db.scalars(select(Job).where(Job.kind == "chat", Job.state == "done")
                                       .order_by(Job.id.desc()).limit(3)))
            db.add(Job(key="chat:" + uuid4().hex, kind="chat", priority=1,
                       payload={"question": question.strip(), "month": month,
                                "previous": [row.payload for row in reversed(previous)]}))
            return jsonify(status="pending"), 202
        control = db.get(Runtime, "codex")
        jobs = list(db.scalars(select(Job).where(Job.kind == "chat").order_by(Job.id.desc()).limit(8)))
        return jsonify(messages=[{"question": job.payload["question"], "month": job.payload["month"],
                                  "answer": job.payload.get("answer"), "state": job.state, "notice": job.notice}
                                 for job in reversed(jobs)],
                       waiting=active_codex_notice(control))


@bp.get("/api/economy")
def economy_api():
    with db_session() as db:
        window = Event.date.between(day() - timedelta(days=60), day() + timedelta(days=60))
        selected = request.args.get("evento", type=int)
        if selected and 0 < selected < 2 ** 31:
            window = or_(window, Event.id == selected)
        events = db.scalars(select(Event).where(window).order_by(Event.date, Event.id))
        sources = []
        for name, (url, _) in FEEDS.items():
            record = db.get(Runtime, name)
            sources.append({"name": name, "url": url, "checked_at": iso(record.checked_at) if record else None,
                            "error": record.notice if record else ""})
        return jsonify(events=[event_data(e) for e in events], sources=sources)


@bp.get("/api/agents")
def agents_api():
    with db_session() as db:
        settings = db.get(CodexSettings, 1)
        control = db.get(Runtime, "codex")
        worker = db.get(Runtime, "worker")
        queue = {state: 0 for state in ("pending", "running", "done", "retry", "uncertain")}
        queue.update(dict(db.execute(select(Job.state, func.count()).group_by(Job.state)).all()))
        latest = db.scalar(select(Job.notice).where(Job.state.in_(("retry", "uncertain")))
                           .order_by(Job.id.desc()).limit(1))
        return jsonify(enabled=bool(worker and worker.count), queue=queue, worker_seen_at=iso(worker.checked_at) if worker else None,
                       roles=AGENTS, models=settings.models if settings else {},
                       preferences=settings.preferences if settings else {},
                       last_success=iso(control.checked_at) if control else None,
                       notice=active_codex_notice(control) or latest or "")


@bp.post("/api/agents/<kind>/settings")
def save_agent_settings(kind):
    if kind not in AGENTS:
        return jsonify(error="Agente desconhecido."), 404
    data = request.get_json(silent=True)
    if not isinstance(data, dict) or set(data) != {"model", "effort"} or not all(isinstance(v, str) for v in data.values()):
        return jsonify(error="Informe o modelo e o esforço."), 400
    with db_session() as db:
        settings = db.get(CodexSettings, 1, with_for_update=True)
        if not accepts(settings.models if settings else {}, **data):
            return jsonify(error="Escolha um modelo disponível e um esforço aceito por ele."), 400
        if settings:
            preferences = dict(settings.preferences)
            if data["model"]:
                preferences[kind] = data
            else:
                preferences.pop(kind, None)
            settings.preferences = preferences
        db.execute(update(Job).where(Job.kind == kind, Job.state == "retry", Job.notice == model_notice(kind))
                   .values(ready_at=now(), notice=""))
    return jsonify(status="saved")


@bp.get("/api/assets/<int:position_id>")
def asset_api(position_id):
    with db_session() as db:
        pos = db.get(finance.Position, position_id)
        if not pos:
            return jsonify(error="A posição não foi encontrada."), 404
        identifier = public_identifier(pos)
        file = db.get(AssetFile, identifier) if identifier else None
        return jsonify(identifier=pos.identifier, status=file.status if file else "pending" if identifier else "unavailable",
                       fields=file.fields if file else [], checked_at=file.checked_at.isoformat() if file else None,
                       note=file.note if file else "" if identifier else "O extrato não traz um código público reconhecido.")
