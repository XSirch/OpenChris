"""Loopback-only synthetic Django comparator, intentionally smaller than the app."""

import argparse
import asyncio
import atexit
from functools import wraps
import hmac
import json
import os
from pathlib import Path
import threading

from django.conf import settings
from django.core.asgi import get_asgi_application
from django.core.exceptions import RequestDataTooBig
from django.http import HttpResponse, JsonResponse, StreamingHttpResponse
from django.urls import path
from psycopg_pool import ConnectionPool

MAX_BODY_BYTES = 65536
DASHBOARD = Path(__file__).resolve().parents[1] / "web" / "benchmark.html"
_pool = None
_database_url = None
_pool_lock = threading.Lock()

if not settings.configured:
    settings.configure(
        DEBUG=False,
        SECRET_KEY="synthetic-comparator-no-user-sessions",
        ROOT_URLCONF=__name__,
        ALLOWED_HOSTS=["127.0.0.1", "localhost", "testserver"],
        INSTALLED_APPS=[],
        MIDDLEWARE=[],
        DATA_UPLOAD_MAX_MEMORY_SIZE=MAX_BODY_BYTES,
        DEFAULT_CHARSET="utf-8",
    )


def close_database():
    global _pool
    with _pool_lock:
        if _pool is not None:
            _pool.close()
            _pool = None


def configure_database(database_url=None):
    """Reuse a persistent bounded pool; fail startup if PostgreSQL is unavailable."""
    global _pool, _database_url
    database_url = database_url or os.environ.get("DATABASE_URL")
    if not database_url:
        raise RuntimeError("DATABASE_URL is required for the local comparator")
    with _pool_lock:
        if _pool is not None and database_url == _database_url:
            return _pool
        if _pool is not None:
            _pool.close()
        _pool = ConnectionPool(database_url, min_size=1, max_size=4, timeout=5, open=True)
        _pool.wait(timeout=10)
        _database_url = database_url
        return _pool


def initialize_schema():
    with configure_database(_database_url).connection() as connection:
        connection.execute("""
            CREATE TABLE IF NOT EXISTS benchmark_events (
                backend text NOT NULL,
                event_id text NOT NULL,
                payload text NOT NULL,
                created_at timestamptz NOT NULL DEFAULT now(),
                PRIMARY KEY (backend, event_id)
            )
        """)


atexit.register(close_database)


def protected(method):
    def decorate(view):
        @wraps(view)
        def invoke(request):
            expected = "Bearer " + os.environ.get("BENCH_TOKEN", "stack-benchmark-local")
            actual = request.headers.get("Authorization", "")
            if not hmac.compare_digest(actual.encode("utf-8"), expected.encode("utf-8")):
                response = JsonResponse({"error": "unauthorized"}, status=401)
                response["WWW-Authenticate"] = "Bearer"
            elif request.method != method:
                response = JsonResponse({"error": "method not allowed"}, status=405)
                response["Allow"] = method
            else:
                response = view(request)
            response["Cache-Control"] = "no-store"
            response["X-Content-Type-Options"] = "nosniff"
            return response

        return invoke

    return decorate


def health(request):
    if request.method != "GET":
        return JsonResponse({"error": "method not allowed"}, status=405)
    with configure_database(_database_url).connection() as connection:
        connection.execute("SELECT 1").fetchone()
    return JsonResponse({"status": "ok"})


@protected("GET")
def query(request):
    with configure_database(_database_url).connection() as connection:
        count = connection.execute("SELECT count(*) FROM benchmark_events WHERE backend='django'").fetchone()[0]
    return JsonResponse({"count": count})


@protected("POST")
def webhook(request):
    try:
        body = request.body
    except RequestDataTooBig:
        return JsonResponse({"error": "body too large"}, status=413)
    if len(body) > MAX_BODY_BYTES:
        return JsonResponse({"error": "body too large"}, status=413)
    try:
        payload = json.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return JsonResponse({"error": "invalid JSON"}, status=400)
    if not isinstance(payload, dict):
        return JsonResponse({"error": "object required"}, status=400)
    values = [payload.get("event_id"), payload.get("text")]
    if any(not isinstance(value, str) or not value.strip() or "\x00" in value for value in values):
        return JsonResponse({"error": "nonempty event_id and text required"}, status=400)
    try:
        for value in values:
            value.encode("utf-8")
    except UnicodeEncodeError:
        return JsonResponse({"error": "invalid Unicode string"}, status=400)
    with configure_database(_database_url).connection() as connection:
        connection.execute(
            "INSERT INTO benchmark_events(backend,event_id,payload) VALUES('django',%s,%s) ON CONFLICT DO NOTHING",
            tuple(values),
        )
    # The pool's connection context commits before the acknowledgement is created.
    return JsonResponse({"accepted": True})


@protected("GET")
def events(request):
    payload = {"agents": [{"id": f"A{number:02d}", "state": "idle"} for number in range(1, 24)]}

    async def snapshot_stream():
        yield "data: " + json.dumps(payload, separators=(",", ":")) + "\n\n"

    response = StreamingHttpResponse(
        snapshot_stream(),
        content_type="text/event-stream",
    )
    response["X-Accel-Buffering"] = "no"
    return response


@protected("GET")
def dashboard(request):
    return HttpResponse(DASHBOARD.read_bytes(), content_type="text/html; charset=utf-8")


urlpatterns = [
    path("healthz", health),
    path("bench/query", query),
    path("bench/webhook", webhook),
    path("bench/events", events),
    path("bench/dashboard", dashboard),
]

_django_application = get_asgi_application()


async def application(scope, receive, send):
    """ASGI lifecycle plus bounded buffering before Django reads webhook bodies."""
    if scope["type"] == "lifespan":
        while True:
            message = await receive()
            if message["type"] == "lifespan.startup":
                try:
                    await asyncio.to_thread(initialize_schema)
                except Exception as exc:
                    await send({"type": "lifespan.startup.failed", "message": type(exc).__name__})
                    return
                await send({"type": "lifespan.startup.complete"})
            elif message["type"] == "lifespan.shutdown":
                await asyncio.to_thread(close_database)
                await send({"type": "lifespan.shutdown.complete"})
                return
    elif scope["type"] == "http" and scope["path"] == "/bench/webhook":
        buffered = bytearray()
        while True:
            message = await receive()
            if message["type"] == "http.disconnect":
                return
            buffered.extend(message.get("body", b""))
            if len(buffered) > MAX_BODY_BYTES:
                await send({"type": "http.response.start", "status": 413, "headers": [(b"content-type", b"application/json")]})
                await send({"type": "http.response.body", "body": b'{"error":"body too large"}'})
                return
            if not message.get("more_body", False):
                break
        consumed = False

        async def replay():
            nonlocal consumed
            if not consumed:
                consumed = True
                return {"type": "http.request", "body": bytes(buffered), "more_body": False}
            return await receive()

        await _django_application(scope, replay, send)
    else:
        await _django_application(scope, receive, send)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=18481)
    args = parser.parse_args()
    import uvicorn

    uvicorn.run(application, host="127.0.0.1", port=args.port, workers=1, access_log=False, server_header=False)


if __name__ == "__main__":
    main()
