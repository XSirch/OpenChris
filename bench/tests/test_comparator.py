"""Real PostgreSQL tests: removing auth, validation or commit must break these."""

import importlib
import json
import os
from pathlib import Path
import unittest
import uuid
import warnings


class ComparatorTests(unittest.TestCase):
    def setUp(self):
        try:
            self.app = importlib.import_module("bench.django_app")
        except ModuleNotFoundError as exc:
            self.fail(f"Django comparator has not been implemented: {exc}")
        self.database_url = os.environ.get("TEST_DATABASE_URL")
        if not self.database_url:
            self.skipTest("Set TEST_DATABASE_URL to a disposable real PostgreSQL database")
        self.app.configure_database(self.database_url)
        self.app.initialize_schema()
        from django.test import Client

        self.client = Client(raise_request_exception=False)
        self.headers = {"HTTP_AUTHORIZATION": "Bearer stack-benchmark-local"}

    def test_health_checks_database_without_authentication(self):
        response = self.client.get("/healthz")
        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.json(), {"status": "ok"})

    def test_routes_require_exact_bearer_token(self):
        for path in ("query", "webhook", "events", "dashboard"):
            for header in ("", "Bearer wrong", "stack-benchmark-local"):
                with self.subTest(path=path, header=header):
                    response = self.client.get(f"/bench/{path}", HTTP_AUTHORIZATION=header)
                    self.assertEqual(response.status_code, 401)

    def test_webhook_is_durable_and_idempotent(self):
        import psycopg

        event_id = "test-" + str(uuid.uuid4())
        body = {"event_id": event_id, "text": "Validação sintética: memória entre canais."}
        before = self.client.get("/bench/query", **self.headers).json()["count"]
        for _ in range(2):
            response = self.client.post("/bench/webhook", body, content_type="application/json", **self.headers)
            self.assertEqual(response.status_code, 200)
            self.assertEqual(response.json(), {"accepted": True})
        with psycopg.connect(self.database_url) as connection:
            row = connection.execute(
                "SELECT payload FROM benchmark_events WHERE backend='django' AND event_id=%s", (event_id,)
            ).fetchone()
            self.assertEqual(row, (body["text"],))
            connection.execute("INSERT INTO benchmark_events(backend,event_id,payload) VALUES('go',%s,'separate')", (event_id,))
        after = self.client.get("/bench/query", **self.headers).json()["count"]
        self.assertEqual(after, before + 1)

    def test_rejects_malformed_empty_and_oversize_bodies(self):
        cases = [
            (b"{", 400), (b"null", 400), (b"[]", 400),
            (b'{"event_id":"e","text":""}', 400),
            (b'{"event_id":" ","text":"t"}', 400),
            (b'{"event_id":3,"text":"t"}', 400),
            (b'{"event_id":"e","text":" "}', 400),
            (b'{"event_id":"e"}', 400),
            (b"\xff", 400),
            (b'{"event_id":"e","text":"\\u0000"}', 400),
            (b'{"event_id":"e","text":"\\ud800"}', 400),
            (json.dumps({"event_id": "e", "text": "x" * 70000}).encode(), 413),
        ]
        for body, expected in cases:
            with self.subTest(body=body[:60]):
                response = self.client.post("/bench/webhook", body, content_type="application/json", **self.headers)
                self.assertEqual(response.status_code, expected)

    def test_sse_returns_one_snapshot_and_closes(self):
        response = self.client.get("/bench/events", **self.headers)
        self.assertEqual(response.status_code, 200)
        self.assertEqual(response["Content-Type"], "text/event-stream")
        from asgiref.sync import async_to_sync

        async def consume():
            return b"".join([part async for part in response]).decode()

        with warnings.catch_warnings(record=True) as caught:
            warnings.simplefilter("always")
            body = async_to_sync(consume)()
        self.assertEqual([str(item.message) for item in caught], [])
        lines = [line.removeprefix("data: ") for line in body.splitlines() if line.startswith("data: ")]
        self.assertEqual(len(lines), 1)
        agents = json.loads(lines[0])["agents"]
        self.assertEqual(len(agents), 23)
        self.assertEqual(agents[0], {"id": "A01", "state": "idle"})
        self.assertEqual(agents[-1], {"id": "A23", "state": "idle"})

    def test_dashboard_returns_shared_document(self):
        response = self.client.get("/bench/dashboard", **self.headers)
        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.content, (Path(__file__).resolve().parents[2] / "web/benchmark.html").read_bytes())

    def test_unsupported_routes_and_methods(self):
        self.assertEqual(self.client.get("/missing", **self.headers).status_code, 404)
        self.assertEqual(self.client.get("/bench/webhook", **self.headers).status_code, 405)
        self.assertEqual(self.client.post("/bench/query", **self.headers).status_code, 405)


if __name__ == "__main__":
    unittest.main()
