import base64
import io
import re

import pytest
from pypdf import PdfWriter
from pypdf.generic import DecodedStreamObject, DictionaryObject, NameObject
from sqlalchemy import select, text

from openchris import automation
from openchris.db import db_session
from openchris.web import create_app


@pytest.fixture
def owner(tmp_path, monkeypatch):
    password = "document-fixture-only"
    monkeypatch.setenv("DATABASE_URL", "sqlite:///" + str(tmp_path / "documents.db"))
    monkeypatch.setenv("OPENCHRIS_STATE", str(tmp_path))
    monkeypatch.setenv("OPENCHRIS_DOCUMENT_KEY", base64.urlsafe_b64encode(b"d" * 32).decode())
    monkeypatch.setenv("CODEX_ENABLED", "1")
    client = create_app({"TESTING": True, "OWNER_PASSWORD": password, "SESSION_COOKIE_SECURE": False}).test_client()
    assert client.get("/api/documents").status_code == 401
    token = re.search(r'name="csrf_token" value="([^"]+)', client.get("/login").text)[1]
    assert client.post("/login", data={"password": password, "csrf_token": token}).status_code == 302
    with client.session_transaction() as session:
        headers = {"X-CSRF-Token": session["csrf"]}
    with db_session() as db:
        worker = automation.runtime(db, "worker")
        worker.count, worker.checked_at = 1, automation.now()
    return client, headers


def upload(owner, contents, filename="declaração.txt", **fields):
    client, headers = owner
    return client.post("/api/documents", headers=headers, data={"file": (io.BytesIO(contents), filename), **fields})


def ask(owner, question, **fields):
    client, headers = owner
    return client.post("/api/finance-chat", headers=headers, json={
        "question": question, "month": automation.day().strftime("%Y-%m"), **fields,
    })


def pdf(*pages, password=None):
    writer = PdfWriter()
    for lines in pages:
        page = writer.add_blank_page(612, 792)
        if not lines:
            continue
        fonts = DictionaryObject({NameObject("/F1"): DictionaryObject({
            NameObject("/Type"): NameObject("/Font"), NameObject("/Subtype"): NameObject("/Type1"),
            NameObject("/BaseFont"): NameObject("/Helvetica"), NameObject("/Encoding"): NameObject("/WinAnsiEncoding"),
        })})
        page[NameObject("/Resources")] = DictionaryObject({NameObject("/Font"): fonts})
        stream = DecodedStreamObject()
        stream.set_data(("BT /F1 12 Tf 48 730 Td " + " 0 -22 Td ".join(f"({line}) Tj" for line in lines)
                         + " ET").encode("cp1252"))
        page.replace_contents(stream)
    if password:
        writer.encrypt(password)
    result = io.BytesIO()
    writer.write(result)
    return result.getvalue()


def test_documents_are_private_encrypted_and_downloadable(owner):
    client, headers = owner
    original = "Rendimentos tributáveis 72.910,00\nConteúdo exclusivo da declaração.".encode("utf-8")
    saved = upload(owner, original, kind="ir", year="2025")
    assert saved.status_code == 201
    doc = saved.json
    assert doc["name"] == "declaração.txt" and doc["kind"] == "ir" and doc["year"] == 2025
    assert doc["reading"] == "ready" and doc["chat_enabled"] is False
    assert "72.910" not in client.get("/api/documents").text
    with db_session() as db:
        sealed = db.execute(text("SELECT sealed_file, sealed_index, sealed_names FROM documents")).one()
        assert all(isinstance(blob, bytes) for blob in sealed)
        assert original not in sealed[0] and b"72.910" not in sealed[1] and b"declara" not in sealed[2]
    path = "/api/documents/" + doc["id"]
    download = client.get(path + "/download")
    assert download.data == original and download.headers["Content-Disposition"].startswith("attachment;")
    assert download.headers["Cache-Control"] == "no-store"
    assert client.post(path + "/delete").status_code == 403
    assert client.post(path + "/delete", json={}, headers=headers).status_code == 200
    assert client.get(path + "/download").status_code == 404
    assert not client.get("/api/documents").json["items"]
    assert client.post("/logout", headers=headers).status_code == 303
    assert client.get(path + "/download").status_code == 401


def test_chat_uses_only_relevant_authorized_passages_and_cites_pages(owner, monkeypatch):
    client, _ = owner
    sample = pdf(("Identificação CPF 123.456.789-10", "Página sem informação tributária relevante"),
                 ("Rendimentos tributáveis R$ 84500,00", "Previdência oficial R$ 7200,00",
                  "Opção de tributação por deduções legais", "CPF 123.456.789-10"))
    ir = upload(owner, sample, "IR de teste.pdf", kind="ir", year="2025", chat_enabled="on")
    assert ir.status_code == 201 and ir.json["reading"] == "ready"
    assert upload(owner, b"Rendimentos tributaveis R$ 999999 PRIVADO", "privado.txt", kind="ir").status_code == 201
    assert upload(owner, b"Manutencao da bicicleta UNRELATED", "bike.txt", kind="other", chat_enabled="on").status_code == 201
    captures = []

    def answer(kind, data, **options):
        assert kind == "chat"
        captures.append(data)
        return {"answer": "Resposta de teste com 84500,00." if data["documentos"] else "Nenhum trecho consultado.", "event_ids": [],
                "document_ids": [*(part["ref"] for part in data["documentos"]), "inventado"]}

    monkeypatch.setattr(automation, "codex_result", answer)
    assert ask(owner, "PGBL faria sentido para o meu perfil?").status_code == 202
    assert automation.run_one()
    evidence, = captures[0]["documentos"]
    assert evidence["pagina"] == 2 and "84500,00" in evidence["trecho"]
    payload = str(captures[0])
    assert all(term not in payload for term in ("123.456.789-10", "PRIVADO", "UNRELATED", "IR de teste.pdf"))
    message, = client.get("/api/finance-chat").json["messages"]
    source, = message["document_references"]
    assert source["name"] == "IR de teste.pdf" and source["page"] == 2 and source["year"] == 2025
    with db_session() as db:
        history = str(db.scalar(select(automation.Job)).payload)
        assert "Rendimentos tributáveis" not in history and "IR de teste.pdf" not in history
    assert ask(owner, "Quanto gastei com supermercado?").status_code == 202
    assert automation.run_one()
    assert not captures[1]["documentos"]
    assert "84500,00" not in str(captures[1]["conversa_anterior"])
    assert ask(owner, "Qual é a diferença entre PGBL e VGBL?").status_code == 202
    assert automation.run_one() and not captures[2]["documentos"]
    assert ask(owner, "PGBL faria sentido para mim?", document="none").status_code == 202
    assert automation.run_one() and not captures[3]["documentos"]
    assert ask(owner, "Leia o arquivo escolhido", document=ir.json["id"]).status_code == 202
    assert automation.run_one() and {x["pagina"] for x in captures[4]["documentos"]} == {1, 2}


def test_chat_respects_revoked_access_to_a_queued_document(owner, monkeypatch):
    client, headers = owner
    saved = upload(owner, b"Rendimentos tributaveis 90000", kind="ir", chat_enabled="on")
    path = "/api/documents/" + saved.json["id"]
    assert ask(owner, "Considere este documento", document=saved.json["id"]).status_code == 202
    assert client.post(path + "/chat", json={"enabled": False}, headers=headers).status_code == 200

    def answer(kind, data, **options):
        assert not data["documentos"] and data["aviso_documentos"]
        return {"answer": "A declaração não estava disponível.", "event_ids": [], "document_ids": []}

    monkeypatch.setattr(automation, "codex_result", answer)
    assert automation.run_one()
    assert client.get("/api/finance-chat").json["messages"][0]["document_notice"]
    assert ask(owner, "Considere este documento", document=saved.json["id"]).status_code == 409


def test_image_only_protected_and_invalid_files(owner):
    client, headers = owner
    blank = upload(owner, pdf(None), "escaneado.pdf", kind="other", chat_enabled="on")
    assert blank.status_code == 201 and blank.json["reading"] == "no_text" and not blank.json["chat_enabled"]
    assert client.post("/api/documents/" + blank.json["id"] + "/chat",
                       json={"enabled": True}, headers=headers).status_code == 409
    locked = upload(owner, pdf(("Rendimento privado",), password="teste"), "protegido.pdf", kind="ir")
    assert locked.status_code == 201 and locked.json["reading"] == "locked"
    assert upload(owner, b"<script>conteudo</script>", "arquivo.html", kind="other").status_code == 400
    assert upload(owner, bytes([0, 1, 2, 3]), "arquivo.txt", kind="other").status_code == 400
    assert upload(owner, b"Not really a PDF", "arquivo.pdf", kind="other").status_code == 400
    assert upload(owner, b"a" * (5 * 1024 * 1024 + 1), "grande.txt", kind="other").status_code == 413
