"""Private document storage and local passage retrieval for the finance chat."""

import hashlib
import io
import json
import os
import re
import subprocess
import sys
from collections import Counter
from datetime import datetime, timezone
from pathlib import PurePosixPath
from unicodedata import category, normalize
from uuid import UUID, uuid4

from cryptography.fernet import Fernet, InvalidToken
from flask import Blueprint, jsonify, request, send_file
from sqlalchemy import Boolean, DateTime, LargeBinary, String, func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Mapped, mapped_column

from .db import Base, db_session
from .document_text import INDEX_CHARS

bp = Blueprint("documents", __name__)
MAX_FILE_BYTES = 5 * 1024 * 1024
MAX_DOCUMENTS = 40
KINDS = {"ir": "Declaração de IR", "income": "Informe de rendimentos", "other": "Outro documento"}
MIME_TYPES = {".pdf": "application/pdf", ".txt": "text/plain"}
READABLE = {"ready", "partial"}
STORAGE_ERROR = "O armazenamento de documentos está indisponível."
NO_DOCUMENT = "O documento escolhido não está disponível para consulta no chat."


class Document(Base):
    __tablename__ = "documents"
    id: Mapped[str] = mapped_column(String(36), primary_key=True, default=lambda: str(uuid4()))
    sha256: Mapped[str] = mapped_column(String(64), unique=True)
    kind: Mapped[str] = mapped_column(String(12))
    year: Mapped[int | None]
    media_type: Mapped[str] = mapped_column(String(40))
    size: Mapped[int]
    reading: Mapped[str] = mapped_column(String(12))
    chat_enabled: Mapped[bool] = mapped_column(Boolean, default=False)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=lambda: datetime.now(timezone.utc).replace(tzinfo=None))
    sealed_names: Mapped[bytes] = mapped_column(LargeBinary)
    sealed_file: Mapped[bytes] = mapped_column(LargeBinary, deferred=True)
    sealed_index: Mapped[bytes] = mapped_column(LargeBinary, deferred=True)


def _cipher():
    key = os.getenv("OPENCHRIS_DOCUMENT_KEY", "")
    try:
        return Fernet(key.encode("ascii")) if key else None
    except ValueError:
        return None


def _sealed_json(cipher, value):
    return cipher.encrypt(json.dumps(value, ensure_ascii=False).encode("utf-8"))


def _names(doc, cipher):
    return json.loads(cipher.decrypt(doc.sealed_names)) if cipher else None


def _metadata(doc, names=None):
    return {"id": doc.id, "name": names["name"] if names else "Documento indisponível",
            "kind": doc.kind, "year": doc.year, "size": doc.size,
            "reading": doc.reading if names else "unavailable",
            "chat_enabled": doc.chat_enabled and bool(names),
            "created_at": doc.created_at.isoformat(timespec="seconds") + "Z"}


def chat_source_labels(db, references, names):
    cipher = _cipher()
    for source in references:
        doc_id = source["id"]
        if doc_id not in names:
            doc = db.get(Document, doc_id)
            try:
                name = _names(doc, cipher) if doc else None
            except InvalidToken:
                name = None
            names[doc_id] = name["name"] if name else ("Documento indisponível" if doc else "Documento removido")
        yield {"name": names[doc_id], "year": source["year"], "page": source["page"]}


def _clean_name(value):
    return "".join(char for char in normalize("NFC", value) if category(char) not in {"Cc", "Cf", "Cs"}).strip()[:200]


def _index_file(source, suffix):
    if suffix == ".txt":
        try:
            body = source.decode("utf-8-sig")
        except UnicodeDecodeError:
            raise ValueError("Salve o arquivo TXT em UTF-8.") from None
        if any(ord(char) < 32 and char not in "\t\r\n" for char in body):
            raise ValueError("O arquivo precisa conter texto legível.")
        body = body.replace("\r\n", "\n").replace("\r", "\n").strip()
        if not any(char.isalnum() for char in body):
            return "no_text", []
        return ("partial" if len(body) > INDEX_CHARS else "ready"), [[None, body[:INDEX_CHARS]]]
    if not source.startswith(b"%PDF-"):
        raise ValueError("O arquivo não abriu como PDF.")
    # Bound PDF parsing time and isolate memory use from the web process.
    env = {key: os.environ[key] for key in ("PATH", "SYSTEMROOT", "WINDIR", "TMP", "TEMP", "LANG") if key in os.environ}
    try:
        result = subprocess.run([sys.executable, "-m", "openchris.document_text"], input=source,
                                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=13, env=env)
        if result.returncode:
            return "unreadable", []
        status, pages = json.loads(result.stdout)
    except (OSError, subprocess.TimeoutExpired, ValueError):
        return "unreadable", []
    if status == "invalid":
        raise ValueError("O arquivo não abriu como PDF. Verifique se a cópia está completa.")
    return status, pages


@bp.route("/api/documents", methods=["GET", "POST"])
def library():
    cipher = _cipher()
    if request.method == "GET":
        with db_session() as db:
            items, available = [], bool(cipher)
            for doc in db.scalars(select(Document).order_by(Document.created_at.desc(), Document.id)):
                try:
                    names = _names(doc, cipher)
                except InvalidToken:
                    names, available = None, False
                items.append(_metadata(doc, names))
        return jsonify(items=items, available=available, kinds=KINDS,
                       max_file_size=MAX_FILE_BYTES, max_documents=MAX_DOCUMENTS)
    if not cipher:
        return jsonify(error=STORAGE_ERROR), 503

    request.max_content_length = MAX_FILE_BYTES + 64 * 1024
    received = request.files.get("file")
    if not received or not received.filename:
        return jsonify(error="Escolha um arquivo para guardar."), 400
    original_name = PurePosixPath(received.filename.replace("\\", "/")).name
    suffix = PurePosixPath(original_name).suffix.lower()
    if suffix not in MIME_TYPES:
        return jsonify(error="Escolha um arquivo PDF ou TXT."), 400
    filename = _clean_name(PurePosixPath(original_name).stem)[:190] + suffix
    kind = request.form.get("kind", "other")
    year = request.form.get("year", "").strip()
    if kind not in KINDS or (year and (not re.fullmatch(r"[0-9]{4}", year) or not 1900 <= int(year) <= 2100)):
        return jsonify(error="Confira o tipo do documento e o ano dos dados."), 400
    source = received.read(MAX_FILE_BYTES + 1)
    if not source:
        return jsonify(error="O arquivo está vazio."), 400
    if len(source) > MAX_FILE_BYTES:
        return jsonify(error="O arquivo excede 5 MB."), 413
    checksum = hashlib.sha256(source).hexdigest()
    try:
        reading, pages = _index_file(source, suffix)
    except ValueError as error:
        return jsonify(error=str(error)), 400
    names = {"name": _clean_name(request.form.get("name", "")) or filename, "filename": filename}
    try:
        with db_session() as db:
            if db.bind.dialect.name == "postgresql":
                db.execute(select(func.pg_advisory_xact_lock(73148613)))
            if db.scalar(select(func.count(Document.id))) >= MAX_DOCUMENTS:
                return jsonify(error="O espaço para 40 documentos está cheio. Exclua um arquivo antes de adicionar outro."), 409
            first = db.scalar(select(Document.sealed_names).limit(1))
            if first is not None:
                cipher.decrypt(first)
            doc = Document(sha256=checksum, kind=kind, year=int(year) if year else None, size=len(source),
                           media_type=MIME_TYPES[suffix], reading=reading,
                           chat_enabled=reading in READABLE and request.form.get("chat_enabled") == "on",
                           sealed_names=_sealed_json(cipher, names), sealed_file=cipher.encrypt(source),
                           sealed_index=_sealed_json(cipher, pages))
            db.add(doc)
            db.flush()
            result = _metadata(doc, names)
    except InvalidToken:
        return jsonify(error=STORAGE_ERROR), 503
    except IntegrityError:
        return jsonify(error="Esse arquivo já está guardado em Documentos."), 409
    return jsonify(result), 201


@bp.get("/api/documents/<uuid:document_id>/download")
def download(document_id):
    cipher = _cipher()
    if not cipher:
        return jsonify(error=STORAGE_ERROR), 503
    with db_session() as db:
        doc = db.get(Document, str(document_id))
        if not doc:
            return jsonify(error="O documento não foi encontrado."), 404
        try:
            name = _names(doc, cipher)["filename"]
            source = cipher.decrypt(doc.sealed_file)
        except InvalidToken:
            return jsonify(error="Não foi possível abrir o documento guardado."), 503
        return send_file(io.BytesIO(source), download_name=name, mimetype=doc.media_type, as_attachment=True)


@bp.post("/api/documents/<uuid:document_id>/chat")
def permit_chat(document_id):
    data = request.get_json(silent=True)
    if not isinstance(data, dict) or type(data.get("enabled")) is not bool:
        return jsonify(error="Escolha se o chat pode consultar este documento."), 400
    with db_session() as db:
        doc = db.get(Document, str(document_id), with_for_update=True)
        if not doc:
            return jsonify(error="O documento não foi encontrado."), 404
        if data["enabled"] and (not _cipher() or doc.reading not in READABLE):
            return jsonify(error="Este arquivo ainda não pode ser consultado pelo chat."), 409
        doc.chat_enabled = data["enabled"]
    return jsonify(chat_enabled=data["enabled"])


@bp.post("/api/documents/<uuid:document_id>/delete")
def delete(document_id):
    with db_session() as db:
        doc = db.get(Document, str(document_id), with_for_update=True)
        if not doc:
            return jsonify(error="O documento não foi encontrado."), 404
        db.delete(doc)
    return jsonify(deleted=True)


def chat_selection(db, selection):
    if isinstance(selection, str) and selection in ("auto", "none"):
        return selection
    try:
        document_id = str(UUID(selection)) if isinstance(selection, str) else None
    except ValueError:
        document_id = None
    doc = db.get(Document, document_id) if document_id else None
    if not doc or not doc.chat_enabled or doc.reading not in READABLE or not _cipher():
        raise ValueError(NO_DOCUMENT)
    return document_id


_STOP = set("aos das dos para sobre como quanto quantos qual quais esse essa este esta meus suas dos uma meu minha "
            "pelo pela algo onde quem quando seria faz faria sentido analise avaliar use usar documento arquivo "
            "mostre tenho pode faça quero sobre analise com sem vale pena".split())
_TAX_QUERY = re.compile(r"\b(pgbl|vgbl|irpf|previdencia (?:privada|oficial)|imposto de renda|declaracao de (?:ir|ajuste)|"
                        r"declaracao|rendimentos tributaveis|renda tributavel|deducao fiscal|deducoes legais|"
                        r"desconto simplificado|modelo completo)\b")
_PERSONAL_TAX = re.compile(r"\b(meu|minha|meus|minhas|para mim|no meu caso|faria sentido|posso|devo|tenho|"
                           r"paguei|contribui|deduzir quanto|quanto posso)\b")
_TAX_FACTS = ("rendimentos tributaveis", "renda tributavel", "deducoes legais", "desconto simplificado",
              "previdencia oficial", "previdencia privada", "contribuicao previdenciaria", "imposto devido",
              "opcao de tributacao", "modelo completo", "pgbl", "contribuicoes", "tributacao exclusiva")
_SAME_FILE = re.compile(r"\b(esse|este|mesmo|nesse|nesse mesmo|essa|esta|mesma|nessa)\s+(documento|arquivo|informe|declaracao)\b")
_IDENTIFIERS = re.compile(r"(?<!\d)(?:\d{3}\.\d{3}\.\d{3}-\d{2}|\d{2}\.\d{3}\.\d{3}/\d{4}-\d{2})(?!\d)"
                          r"|(?<=CPF[: ])[ ]?\d{11}(?!\d)|[\w.+%-]+@[\w.-]+\.[a-z]{2,}", re.I)


def _fold(value):
    return normalize("NFD", value.casefold()).encode("ascii", "ignore").decode()


def _words(plain):
    return set(re.findall(r"[a-z]{3,}", plain)) - _STOP


def _passages(body, length=1400, overlap=180):
    start = 0
    while start < len(body):
        end = min(start + length, len(body))
        if end < len(body):
            boundary = max(body.rfind("\n", end - 160, end), body.rfind(" ", end - 100, end))
            if boundary > start:
                end = boundary
        yield body[start:end].strip()
        if end == len(body):
            break
        start = end - overlap


def chat_passages(db, question, selection="auto", previous=None):
    """Only this execution sees the extracted text. Jobs retain source metadata."""
    if selection == "none":
        return [], ""
    cipher = _cipher()
    manual = selection != "auto"
    if not cipher:
        return [], NO_DOCUMENT if manual else ""
    plain = _fold(question)
    continuation = not manual and bool(previous and previous.get("consulted_documents") and _SAME_FILE.search(plain))
    if continuation:
        plain += " " + _fold(previous["question"])
    terms = _words(plain)
    tax_question = bool(_TAX_QUERY.search(plain))
    personal_tax = tax_question and bool(_PERSONAL_TAX.search(plain))
    years = {int(y) for y in re.findall(r"\b20\d{2}\b", plain)}
    query = select(Document).where(Document.chat_enabled.is_(True), Document.reading.in_(READABLE))
    if manual:
        query = query.where(Document.id == selection)
    elif continuation:
        query = query.where(Document.id.in_(previous["consulted_documents"]))
    matches = []
    for doc in db.scalars(query.order_by(Document.year.desc().nulls_last(), Document.created_at.desc(), Document.id)):
        try:
            names = _names(doc, cipher)
            text_pages = json.loads(cipher.decrypt(doc.sealed_index))
        except InvalidToken:
            continue
        if not manual and years and doc.year and doc.year not in years:
            continue
        title = _fold(re.sub(r"\.(pdf|txt)$", "", names["name"], flags=re.I))
        title_terms = _words(title)
        named = bool(title_terms and (title_terms <= terms or len(terms & title_terms) >= 2))
        tax_doc = doc.kind in ("ir", "income") or bool(_TAX_QUERY.search(title) or " ir " in " " + title + " ")
        if tax_doc and not (manual or continuation or personal_tax or named):
            continue
        for page, text in text_pages:
            for excerpt in _passages(text):
                normalized = _fold(excerpt)
                lexical = len(terms & _words(normalized))
                tax_hits = sum(phrase in normalized for phrase in _TAX_FACTS) if tax_question and tax_doc else 0
                if not (manual or continuation or named or lexical >= 2 or tax_hits):
                    continue
                if excerpt:
                    score = 4 * lexical + 3 * tax_hits + 2 * named
                    matches.append((score, doc, page, excerpt))
    matches.sort(key=lambda item: item[0], reverse=True)
    excerpts, counts = [], Counter()
    for _, doc, page, text in matches:
        if counts[doc.id] == 3:
            continue
        counts[doc.id] += 1
        excerpts.append({"ref": f"D{len(excerpts) + 1}", "document_id": doc.id,
                         "tipo": KINDS[doc.kind], "ano": doc.year, "pagina": page,
                         "leitura_parcial": doc.reading == "partial", "trecho": _IDENTIFIERS.sub("[omitido]", text)})
        if len(excerpts) == 6:
            break
    return excerpts, NO_DOCUMENT if (manual or continuation) and not excerpts else ""
