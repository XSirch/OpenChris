import json
import subprocess
from datetime import timedelta
from pathlib import Path

import pytest
import requests
from flask import Flask
from sqlalchemy import select

from openchris import agent_runner as runner, automation as jobs, banking, economy, finance
from openchris.db import Base, db_session, engine


@pytest.fixture
def site(tmp_path, monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "sqlite:///" + str(tmp_path / "automation.db"))
    monkeypatch.setenv("CODEX_ENABLED", "1")
    monkeypatch.setenv("CODEX_DAILY_LIMIT", "12")
    monkeypatch.delenv("TELEGRAM_ENABLED", raising=False)
    monkeypatch.setattr(jobs, "FEEDS", {})
    monkeypatch.setattr(jobs, "model_catalog", lambda: {"model-test": {"efforts": ["low", "high"], "default": "low"}})
    def offline(*args, **kwargs):
        pytest.fail("Unexpected external call")
    monkeypatch.setattr(requests.sessions.Session, "request", offline)
    monkeypatch.setattr(jobs, "codex_result", offline)
    Base.metadata.create_all(engine())
    app = Flask(__name__)
    app.register_blueprint(jobs.bp)
    return app.test_client()


def entry(db, description, num):
    return finance.upsert_entry(db, {"date": jobs.day().isoformat(), "description": description,
                                "amount": "92.31", "kind": "expense", "external_id": "test:" + str(num)}, "csv")


def test_first_catalog_seeds_supported_agents_once(site, monkeypatch):
    models = {"gpt-6-luna": {"efforts": ["low", "medium"]}, "gpt-6.1-sol": {"efforts": ["medium"]},
              "gpt-6-astra": {"efforts": ["medium"]}}
    monkeypatch.setattr(jobs, "model_catalog", lambda: models)
    jobs.refresh_models()
    assert set(jobs.INITIAL_AGENT_SETTINGS) == set(runner.SCHEMAS)
    expected = dict(jobs.INITIAL_AGENT_SETTINGS)
    expected.pop("risk")
    assert site.get("/api/agents").json["preferences"] == expected
    assert site.post("/api/agents/expense/settings", json={"model": "", "effort": ""}).status_code == 200
    expected.pop("expense")
    with db_session() as db:
        db.get(jobs.CodexSettings, 1).refresh_after = jobs.now()
    jobs.refresh_models()
    assert site.get("/api/agents").json["preferences"] == expected


def test_role_settings_apply_at_dispatch_and_survive_restart(site, monkeypatch):
    jobs.refresh_models()
    url = "/api/agents/expense/settings"
    explicit = {"model": "model-test", "effort": "high"}
    assert site.post(url, json=explicit).status_code == 200
    assert site.post(url, json={**explicit, "effort": "ultra"}).status_code == 400
    assert site.post(url, json={"model": "-c", "effort": ""}).status_code == 400
    assert site.post("/api/agents/telegram/settings", json=explicit).status_code == 404
    status = site.get("/api/agents").json
    assert set(status["roles"]) == set(runner.SCHEMAS)
    assert status["preferences"] == {"expense": explicit}
    with db_session() as db:
        entry(db, "Padaria", 1)
        jobs.plan(db)
        db.get(jobs.CodexSettings, 1).models = {}
    assert jobs.run_one()
    with db_session() as db:
        job = db.scalar(select(jobs.Job))
        assert job.state == "retry" and job.attempts == 0
        assert db.get(jobs.Runtime, "quota:" + jobs.day().isoformat()) is None
        db.get(jobs.CodexSettings, 1).refresh_after = jobs.now()
    jobs.refresh_models()
    assert site.post(url, json=explicit).status_code == 200
    calls = []
    def classify(kind, batch, **options):
        calls.append(options)
        return {"items": [dict(id=e["id"], kind="expense", category="Mercado", confident=True) for e in batch]}
    monkeypatch.setattr(jobs, "codex_result", classify)
    assert jobs.run_one() and calls == [explicit]
    assert site.post(url, json={"model": "", "effort": ""}).status_code == 200
    assert site.get("/api/agents").json["preferences"] == {}
    with db_session() as db:
        entry(db, "Outra compra", 2)
        jobs.plan(db)
    assert jobs.run_one() and calls == [explicit, {}]


def test_batch_preserves_manual_changes_and_pending(site, monkeypatch):
    with db_session() as db:
        a, b, c = (entry(db, v, n) for n, v in enumerate(["Padaria CPF 123.456.789-00", "Compra", "Almoço"]))
        entry(db, "Arquivo", 4).active = False
    calls = []
    def fake(kind, data):
        calls.append(kind)
        assert [e["id"] for e in data] == [a.id, b.id, c.id]
        assert "123.456" not in json.dumps(data) and "92.31" not in json.dumps(data)
        with db_session() as db:
            finance.classify_entry(db, c.id, "expense", "Refeição", source="manual")
        return {"items": [{"id": d["id"], "kind": "expense", "category": "Alimentação", "confident": d["id"] != b.id}
                          for d in data]}
    monkeypatch.setattr(jobs, "codex_result", fake)
    jobs.tick()
    jobs.tick()
    with db_session() as db:
        assert db.get(finance.Entry, a.id).category == "Alimentação"
        assert db.get(finance.Entry, b.id).classification_source == "pending"
        assert db.get(finance.Entry, c.id).category == "Refeição"
        assert db.get(finance.Entry, a.id).original_description == "Padaria CPF 123.456.789-00"
    assert calls == ["expense"]
    monkeypatch.delenv("CODEX_ENABLED")
    assert site.get("/api/agents").json["enabled"] is True


def test_quota_retry_and_restart(site, monkeypatch):
    with db_session() as db:
        entry(db, "Teste", 1)
    monkeypatch.setenv("CODEX_DAILY_LIMIT", "0")
    jobs.tick()
    assert site.get("/api/agents").json["queue"]["pending"] == 1
    monkeypatch.setenv("CODEX_DAILY_LIMIT", "2")
    with db_session() as db:
        db.get(jobs.Runtime, "codex").until = jobs.now() - timedelta(seconds=1)
    def limit(*args):
        raise runner.AgentPause("Renove o login.", 30)
    monkeypatch.setattr(jobs, "codex_result", limit)
    jobs.tick()
    assert site.get("/api/agents").json["notice"] == "Renove o login."
    with db_session() as db:
        row = db.scalar(select(jobs.Job))
        assert row.state == "retry" and row.attempts == 1
        assert db.get(jobs.Runtime, "quota:" + jobs.day().isoformat()).count == 1
        row.state, row.ready_at = "running", jobs.now() - timedelta(minutes=1)
        db.get(jobs.Runtime, "codex").until = jobs.now() - timedelta(seconds=1)
    monkeypatch.setattr(jobs, "codex_result", lambda k, data: {
        "items": [dict(id=data[0]["id"], kind="expense", category="Mercado", confident=True)]})
    jobs.tick()
    assert site.get("/api/agents").json["queue"]["done"] == 1


def test_login_preflight_refunds_one_reservation_for_whole_batch(site, monkeypatch):
    with db_session() as db:
        entry(db, "Primeira compra", 1)
        entry(db, "Segunda compra", 2)
    monkeypatch.setenv("CODEX_DAILY_LIMIT", "1")
    monkeypatch.setattr(jobs, "codex_result", runner.codex_result)
    monkeypatch.setattr(runner.shutil, "which", lambda name: "/usr/bin/codex")
    def missing_login(args, **kwargs):
        assert args[1:] == ["login", "status"]
        return subprocess.CompletedProcess(args, 1, "", "Not logged in")
    monkeypatch.setattr(runner.subprocess, "run", missing_login)
    jobs.tick()
    with db_session() as db:
        assert db.get(jobs.Runtime, "quota:" + jobs.day().isoformat()).count == 0
        assert [row.state for row in db.scalars(select(jobs.Job))] == ["retry", "retry"]
    assert "ChatGPT" in site.get("/api/agents").json["notice"]


def test_expense_direction_guard_and_independent_batch_results(site, monkeypatch):
    with db_session() as db:
        for i, direction in enumerate(("inflow", "outflow", "outflow")):
            finance.upsert_entry(db, {"external_id": str(i), "description": "Movimento de teste", "amount": "40",
                                 "date": jobs.day().isoformat(), "bank_direction": direction}, "pluggy")
    def fake(kind, data):
        assert [item["bank_direction"] for item in data] == ["inflow", "outflow", "outflow"]
        return {"items": [dict(id=item["id"], kind=kind, category="Teste", confident=True)
                          for item, kind in zip(data, ("expense", "income", "expense"))]}
    monkeypatch.setattr(jobs, "codex_result", fake)
    jobs.tick()
    with db_session() as db:
        assert [item.classification_source for item in db.scalars(select(finance.Entry).order_by(finance.Entry.id))
                ] == ["pending", "pending", "codex"]


def test_research_reused_and_evidence_checked(site, monkeypatch):
    identifier, url = "BRTEST000001", "https://www.b3.com.br/titulo"
    def add(db, num):
        return finance.upsert_position(db, {"name": "Título do extrato", "identifier": identifier, "value": "91",
                                       "external_id": str(num), "asset_class": "Renda fixa"}, "csv")
    with db_session() as db:
        first = add(db, 1)
    calls = []
    def research(kind, data):
        calls.append(kind)
        assert sorted(data) == ["domínios", "identificador"] and data["identificador"] == identifier
        return {"fields": [
            {"field": "issuer", "value": "Banco Teste", "source_url": url,
             "quote": "BRTEST000001. Emissor: Banco Teste", "reference_date": "2026-03-01"},
            {"field": "maturity", "value": "2030-01-20", "source_url": url,
             "quote": "Vencimento: 20/01/2030", "reference_date": "2026-03-01"},
            {"field": "indexer", "value": "CDI", "source_url": "https://site-invalido.example/a",
             "quote": "Índice: CDI", "reference_date": ""}], "note": ""}
    monkeypatch.setattr(jobs, "codex_result", research)
    monkeypatch.setattr(runner, "source_document", lambda u: "01/03/2026 BRTEST000001. Emissor: Banco Teste")
    jobs.tick()
    with db_session() as db:
        other = add(db, 2)
        assert not db.get(finance.Position, first.id).maturity
    jobs.tick()
    card = site.get("/api/assets/" + str(other.id)).json
    assert [f["field"] for f in card["fields"]] == ["issuer"]
    assert card["status"] == "partial" and "vencimento" in card["note"]
    assert card["fields"][0]["reference_date"] == "2026-03-01"
    with db_session() as db:
        assert db.get(finance.Position, other.id).field_sources["issuer"] == url
    assert calls == ["asset"]
    assert site.get("/api/assets/999").status_code == 404


def test_event_lifecycle_debate_and_opt_in(site, monkeypatch):
    url = "https://www.ibge.gov.br/calendario"
    releases = [("ibge:1", jobs.day(), "IPCA", None, url)]
    monkeypatch.setattr(jobs, "FEEDS", {"IBGE": (url, lambda d: releases)})
    calls, sent = [], []
    def expert(kind, context):
        calls.append(kind)
        if kind == "report":
            assert len(context["argumentos"]) == 2
            return dict(conclusion="A inflação afeta títulos indexados.", divergence="Há acordo sobre o mecanismo.",
                        impact="A carteira ainda não tem títulos identificados.", scenarios=["Se o índice subir…"])
        if kind == "risk":
            assert context["argumentos"][0]["specialist"] == "Economia"
        return dict(argument="O índice mede a variação dos preços.", scenario="Se os preços subirem…",
                    source_url=context["evento"]["source_url"])
    monkeypatch.setattr(jobs, "codex_result", expert)
    monkeypatch.setattr(jobs, "send_telegram", lambda data: sent.append(data) or {})
    jobs.tick()
    first = site.get("/api/economy").json["events"][0]
    assert first["phase"] == "scheduled" and first["fact"] is None and not sent
    monkeypatch.setenv("TELEGRAM_ENABLED", "1")
    monkeypatch.setenv("TELEGRAM_BOT_TOKEN", "test")
    monkeypatch.setenv("TELEGRAM_CHAT_ID", "0")
    monkeypatch.setenv("PUBLIC_BASE_URL", "https://test.example")
    releases[0] = ("ibge:1", jobs.day(), "IPCA", "O IPCA foi de 0,3%.", "https://www.ibge.gov.br/release")
    with db_session() as db:
        db.get(jobs.Runtime, "IBGE").until = jobs.now() - timedelta(minutes=1)
    jobs.tick()
    jobs.tick()
    event = site.get("/api/economy").json["events"][0]
    assert event["phase"] == "published" and event["previous_analysis"] == first["analysis"]
    assert len(event["analysis"]["arguments"]) == 2 and event["fact"] == releases[0][3]
    assert len(sent) == 1 and sent[0]["id"] == first["id"]
    assert calls == ["macro", "risk", "report"] * 2


def test_failure_keeps_source_and_ambiguous_telegram_isnt_repeated(site, monkeypatch):
    url = "https://www.ibge.gov.br/"
    monkeypatch.setattr(jobs, "FEEDS", {"IBGE": (url, lambda d: [("ik", d, "IPCA", "Publicado", url)])})
    jobs.refresh_feeds()
    def offline(day):
        raise requests.ConnectionError()
    monkeypatch.setattr(jobs, "FEEDS", {"IBGE": (url, offline)})
    with db_session() as db:
        db.get(jobs.Runtime, "IBGE").until = jobs.now()
        event = db.scalar(select(jobs.Event))
        event.analysis = {"conclusion": "Exemplo"}
        version = dict(id=event.id, signature=jobs.signature(event))
        jobs.enqueue(db, "telegram", version, version, 0)
    monkeypatch.setattr(jobs, "telegram_enabled", lambda: True)
    monkeypatch.setattr(jobs, "send_telegram", lambda data: (_ for _ in ()).throw(requests.ReadTimeout()))
    jobs.tick()
    assert site.get("/api/economy").json["sources"][0]["error"]
    assert site.get("/api/economy").json["events"][0]["fact"] == "Publicado"
    assert site.get("/api/agents").json["queue"]["uncertain"] == 1
    assert not jobs.run_one()


def test_cli_authentication_and_execution_contract(site, monkeypatch, tmp_path):
    monkeypatch.setenv("CODEX_HOME", str(tmp_path))
    monkeypatch.setenv("OPENAI_API_KEY", "synthetic-api-test")
    monkeypatch.setenv("TELEGRAM_BOT_TOKEN", "synthetic-bot-test")
    monkeypatch.setattr(runner.shutil, "which", lambda name: "/usr/bin/codex")
    calls = []
    login = ["Logged in using an API key"]
    def cli(args, **kw):
        calls.append(args)
        assert "OPENAI_API_KEY" not in kw["env"] and "TELEGRAM_BOT_TOKEN" not in kw["env"]
        if args[1:3] == ["login", "status"]:
            return subprocess.CompletedProcess(args, 0, "", login[0])
        assert "read-only" in args and 'forced_login_method="chatgpt"' in args and "--ignore-user-config" in args
        assert '--output-schema' in args and 'web_search="disabled"' in args
        Path(args[args.index("--output-last-message") + 1]).write_text('{"items":[]}', encoding="utf-8")
        return subprocess.CompletedProcess(args, 0, "", "")
    monkeypatch.setattr(runner.subprocess, "run", cli)
    with pytest.raises(runner.PreflightPause):
        runner.codex_result("expense", [])
    assert len(calls) == 1
    login[0] = "Logged in using ChatGPT"
    assert runner.codex_result("expense", []) == {"items": []}
    assert "--model" not in calls[-1] and not any("model_reasoning_effort" in arg for arg in calls[-1])
    runner.codex_result("expense", [], model="model-test", effort="high")
    assert calls[-1][calls[-1].index("--model") + 1] == "model-test"
    assert 'model_reasoning_effort="high"' in calls[-1] and calls[-1][-1] == "-"


def test_public_feed_requires_matching_release(site, monkeypatch):
    today, url = jobs.day(), "http://agenciadenoticias.ibge.gov.br/publicacao"
    def fake(url_endpoint, **params):
        def format_date(n):
            return (today + timedelta(days=n)).strftime("%d/%m/%Y %H:%M:%S")
        if "noticias" in url_endpoint:
            return {"nextPage": 0, "items": [{"produtos_relacionados": "9256, 9258",
                     "data_publicacao": format_date(-2), "link": url, "titulo": "IPCA de 0,3%"}]}
        return {"nextPage": 0, "items": [{"id": i, "produto_id": 9256, "data_divulgacao": format_date(n)}
                                          for i, n in enumerate([-2, -1, 1])]}
    monkeypatch.setattr(economy, "official_json", fake)
    items = economy.ibge_events(today)
    assert [i[3] for i in items] == ["IPCA de 0,3%", None, None]
    assert items[0][4] == url.replace("http:", "https:")
