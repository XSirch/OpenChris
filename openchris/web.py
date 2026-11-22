import hashlib
import hmac
import os
import secrets
import time
from collections import OrderedDict
from datetime import timedelta
from pathlib import Path
from threading import Lock
from urllib.parse import urlsplit

from flask import Flask, abort, jsonify, redirect, render_template, request, session, url_for
from sqlalchemy import text
from werkzeug.security import check_password_hash, generate_password_hash

from .db import db_session, initialize


def session_key():
    if os.getenv("SESSION_SECRET"):
        return os.environ["SESSION_SECRET"]
    path = Path(os.getenv("OPENCHRIS_STATE", "instance")) / "session.key"
    path.parent.mkdir(exist_ok=True, parents=True)
    if not path.exists():
        try:
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w", encoding="ascii") as handle:
                handle.write(secrets.token_hex(48))
        except FileExistsError:
            pass
    key = path.read_text(encoding="ascii").strip()
    if len(key) < 32:
        raise RuntimeError("O segredo da sessão está incompleto.")
    return key


def create_app(config=None):
    app = Flask(__name__)
    app.config.update(SECRET_KEY=session_key(), MAX_CONTENT_LENGTH=5 * 1024 * 1024,
                      SESSION_COOKIE_HTTPONLY=True, SESSION_COOKIE_SAMESITE="Lax",
                      SESSION_COOKIE_SECURE=os.getenv("COOKIE_SECURE", "1") == "1",
                      PERMANENT_SESSION_LIFETIME=timedelta(hours=12))
    if config:
        app.config.update(config)
    password = app.config.get("OWNER_PASSWORD") or os.getenv("OPENCHRIS_PASSWORD", "")
    if len(password) < 12:
        raise RuntimeError("Defina OPENCHRIS_PASSWORD com pelo menos 12 caracteres.")
    password_hash = generate_password_hash(password)
    auth_epoch = hmac.new(app.secret_key.encode(), password.encode(), hashlib.sha256).hexdigest()
    failures = OrderedDict()
    login_lock = Lock()
    from . import automation, banking, documents, finance_api
    for blueprint in (finance_api.bp, banking.bp, automation.bp, documents.bp):
        app.register_blueprint(blueprint)
    if not app.config.get("SKIP_INIT"):
        initialize()

    @app.before_request
    def guard():
        if request.endpoint in ("static", "health") or request.endpoint is None:
            return
        if request.endpoint != "login" and session.get("owner") != auth_epoch:
            if request.path.startswith("/api/"):
                return jsonify(error="Entre novamente para continuar."), 401
            return redirect(url_for("login", next=request.full_path.rstrip("?")))
        if "csrf" not in session:
            session["csrf"] = secrets.token_urlsafe(32)
        if request.method not in {"GET", "HEAD", "OPTIONS"}:
            token = request.headers.get("X-CSRF-Token") or request.form.get("csrf_token", "")
            if not hmac.compare_digest(token.encode(), session["csrf"].encode()):
                abort(403)

    @app.after_request
    def protect(response):
        response.headers["X-Content-Type-Options"] = "nosniff"
        response.headers["X-Frame-Options"] = "DENY"
        response.headers["Referrer-Policy"] = "same-origin"
        if request.endpoint != "static":
            response.headers["Cache-Control"] = "no-store"
        return response

    @app.errorhandler(413)
    def too_large(error):
        return jsonify(error="O arquivo excede 5 MB."), 413

    @app.errorhandler(403)
    def forbidden(error):
        return jsonify(error="A sessão mudou. Atualize a página e tente novamente."), 403

    @app.get("/readyz")
    def health():
        with db_session() as db:
            db.execute(text("SELECT 1"))
        return jsonify(status="ok")

    @app.route("/login", methods=["GET", "POST"])
    def login():
        error = None
        if request.method == "POST":
            address = request.remote_addr or ""
            now = time.monotonic()
            with login_lock:
                count, start = failures.get(address, (0, now))
                if now - start >= 300:
                    count, start = 0, now
                if count >= 8:
                    return render_template("login.html", csrf_token=session["csrf"],
                                           error="Muitas tentativas. Aguarde cinco minutos."), 429
                failures[address] = (count + 1, start)
                failures.move_to_end(address)
                if len(failures) > 4096:
                    failures.popitem(last=False)
            if check_password_hash(password_hash, request.form.get("password", "")):
                with login_lock:
                    failures.pop(address, None)
                session.clear()
                session.update(owner=auth_epoch, csrf=secrets.token_urlsafe(32))
                session.permanent = True
                dest = request.args.get("next", "/")
                if (not dest.startswith("/") or dest.startswith("//") or "\\" in dest or
                        urlsplit(dest).path not in {"/", "/hoje", "/carteira", "/gastos", "/aportes",
                                                    "/mais", "/economia", "/calculadoras", "/documentos"}):
                    dest = "/"
                return redirect(dest)
            error = "Senha incorreta."
        return render_template("login.html", csrf_token=session["csrf"], error=error), 401 if error else 200

    @app.post("/logout")
    def logout():
        session.clear()
        return redirect(url_for("login"), code=303)

    @app.get("/")
    @app.get("/<any(hoje,carteira,gastos,aportes,mais,economia,calculadoras,documentos):area>")
    def index(area=None):
        return render_template("index.html", csrf_token=session["csrf"])

    return app
