"""Exercise the harness against real local HTTP sockets and real process metrics."""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib
import json
import os
import threading
import unittest


class FixtureHandler(BaseHTTPRequestHandler):
    event_ids = []
    requests_seen = []

    def log_message(self, *args):
        pass

    def do_GET(self):
        self.requests_seen.append((self.path, self.headers.get("Authorization")))
        if self.path.endswith("query"):
            body, kind, status = b'{"count":1}', "application/json", 200
        elif self.path.endswith("events"):
            body, kind, status = b'data: {"agents":[{"id":"A01","state":"idle"}]}\n\n', "text/event-stream", 200
        elif self.path.endswith("dashboard"):
            body, kind, status = b"<!doctype html><title>Synthetic</title>", "text/html", 200
        else:
            body, kind, status = b'{"status":"ok"}', "application/json", 200
        if self.path.startswith("/fail"):
            status = 503
        self.send_response(status)
        self.send_header("Content-Type", kind)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        self.requests_seen.append((self.path, self.headers.get("Authorization")))
        payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.event_ids.append(payload["event_id"])
        body = b'{"accepted":true}'
        self.send_response(503 if self.path.startswith("/fail") else 200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class MeasureTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), FixtureHandler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.url = "http://127.0.0.1:" + str(cls.server.server_port)

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    def setUp(self):
        try:
            self.measure = importlib.import_module("bench.measure")
        except ModuleNotFoundError as exc:
            self.fail(f"Benchmark harness has not been implemented: {exc}")
        FixtureHandler.event_ids.clear()
        FixtureHandler.requests_seen.clear()

    def test_percentiles_do_not_invent_measurements(self):
        self.assertEqual(self.measure.summarize([]), {"sample_count": 0, "error_count": 0, "p50_ms": None, "p95_ms": None, "response_bytes_min": None, "response_bytes_max": None})
        rows = [
            {"latency_ms": value, "response_bytes": value * 10, "error": None if value < 4 else "HTTP 503"}
            for value in (1, 2, 3, 4, 100)
        ]
        result = self.measure.summarize(rows)
        self.assertEqual(result["p50_ms"], 3)
        self.assertEqual(result["p95_ms"], 100)
        self.assertEqual(result["error_count"], 2)
        self.assertEqual(result["response_bytes_min"], 10)
        self.assertEqual(result["response_bytes_max"], 1000)

    def test_real_http_rounds_alternate_and_use_unique_events(self):
        report = self.measure.run_benchmark(
            {"go": self.url, "django": self.url}, {"go": os.getpid(), "django": os.getpid()},
            samples=2, rounds=2, warmup=1, token="fixture-token", idle_seconds=0.02,
        )
        self.assertEqual(report["method"]["backend_order"], [["go", "django"], ["django", "go"]])
        self.assertEqual(report["startup"], {"go": None, "django": None})
        self.assertTrue(report["synthetic"])
        self.assertEqual(len(FixtureHandler.event_ids), 10)
        self.assertEqual(len(set(FixtureHandler.event_ids)), 10)
        self.assertTrue(all(header == "Bearer fixture-token" for _, header in FixtureHandler.requests_seen))
        for backend in ("go", "django"):
            self.assertGreater(report["processes"][backend]["idle"]["rss_peak_bytes"], 0)
            for route in ("query", "webhook", "events", "dashboard"):
                summary = report["results"][backend][route]["summary"]
                self.assertEqual(summary["sample_count"], 4)
                self.assertEqual(summary["error_count"], 0)
                self.assertGreater(summary["response_bytes_min"], 0)
                self.assertEqual(len(report["results"][backend][route]["rounds"]), 2)
                load = report["results"][backend][route]["rounds"][0]["process"]
                self.assertGreater(load["rss_peak_bytes"], 0)
                self.assertGreaterEqual(load["cpu_seconds"], 0)

    def test_http_errors_remain_in_evidence(self):
        report = self.measure.run_benchmark(
            {"go": self.url + "/fail", "django": self.url}, {"go": os.getpid(), "django": os.getpid()},
            samples=1, rounds=1, warmup=0, idle_seconds=0.01,
        )
        for route in ("query", "webhook", "events", "dashboard"):
            result = report["results"]["go"][route]
            self.assertEqual(result["summary"]["error_count"], 1)
            self.assertEqual(result["rounds"][0]["samples"][0]["status"], 503)

    def test_rejects_invalid_run_sizes(self):
        for samples, rounds in ((0, 1), (1, 0), (-1, 2)):
            with self.subTest(samples=samples, rounds=rounds), self.assertRaises(ValueError):
                self.measure.run_benchmark({}, {}, samples=samples, rounds=rounds)


if __name__ == "__main__":
    unittest.main()
