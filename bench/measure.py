"""Measure a local, synthetic, single-host Go/Django HTTP contract."""

import argparse
from datetime import datetime, timezone
import importlib.metadata
import json
import math
import os
from pathlib import Path
import platform
import sys
import threading
import time
import uuid

import httpx
import psutil

ROUTES = ("query", "webhook", "events", "dashboard")


def summarize(samples):
    """Nearest-rank percentiles include errors; raw samples retain their status."""
    latencies = sorted(sample["latency_ms"] for sample in samples)
    sizes = [sample["response_bytes"] for sample in samples]
    return {
        "sample_count": len(samples),
        "error_count": sum(sample["error"] is not None for sample in samples),
        "p50_ms": latencies[math.ceil(len(latencies) * 0.50) - 1] if latencies else None,
        "p95_ms": latencies[math.ceil(len(latencies) * 0.95) - 1] if latencies else None,
        "response_bytes_min": min(sizes) if sizes else None,
        "response_bytes_max": max(sizes) if sizes else None,
    }


class ProcessWindow:
    """Sample RSS while measuring CPU time deltas for a PID and its descendants."""

    def __init__(self, pid):
        self.process = psutil.Process(pid)
        self.stopped = threading.Event()
        self.rss_samples = []
        self.metric_errors = []
        self.members = {}

    def snapshot(self):
        rss = 0
        try:
            processes = [self.process, *self.process.children(recursive=True)]
        except psutil.Error as exc:
            self.metric_errors.append(type(exc).__name__)
            return
        for process in processes:
            try:
                identity = (process.pid, process.create_time())
                cpu = process.cpu_times()
                seconds = cpu.user + cpu.system
                if identity not in self.members:
                    self.members[identity] = [seconds, seconds]
                self.members[identity][1] = seconds
                rss += process.memory_info().rss
            except psutil.Error as exc:
                self.metric_errors.append(type(exc).__name__)
        self.rss_samples.append(rss)

    def poll(self):
        while not self.stopped.wait(0.02):
            self.snapshot()

    def __enter__(self):
        self.start = time.perf_counter()
        self.snapshot()
        self.thread = threading.Thread(target=self.poll, daemon=True)
        self.thread.start()
        return self

    def __exit__(self, *args):
        self.stopped.set()
        self.thread.join()
        self.snapshot()
        elapsed = time.perf_counter() - self.start
        seconds = max(0, sum(end - start for start, end in self.members.values()))
        cpu_percent = seconds / elapsed * 100 if elapsed else None
        self.result = {
            "pid": self.process.pid,
            "includes_descendants": True,
            "observed_pids": sorted({identity[0] for identity in self.members}),
            "elapsed_seconds": elapsed,
            "cpu_seconds": seconds,
            "cpu_percent_one_core": cpu_percent,
            "cpu_percent_machine": cpu_percent / (psutil.cpu_count() or 1) if cpu_percent is not None else None,
            "rss_start_bytes": self.rss_samples[0] if self.rss_samples else None,
            "rss_end_bytes": self.rss_samples[-1] if self.rss_samples else None,
            "rss_peak_bytes": max(self.rss_samples) if self.rss_samples else None,
            "rss_sample_count": len(self.rss_samples),
            "rss_sampling_interval_ms": 20,
            "metric_errors": sorted(set(self.metric_errors)),
        }


def request_sample(client, base_url, route):
    start = time.perf_counter_ns()
    status, size, error = None, 0, None
    try:
        if route == "webhook":
            response = client.post(base_url + "/bench/webhook", json={
                "event_id": "benchmark-" + str(uuid.uuid4()),
                "text": "Mensagem sintética para medir persistência local.",
            })
        else:
            response = client.get(base_url + "/bench/" + route)
        status = response.status_code
        size = len(response.content)
        if status != 200:
            error = "HTTP " + str(status)
        elif route == "webhook" and response.json().get("accepted") is not True:
            error = "missing accepted acknowledgement"
        elif route == "query" and not isinstance(response.json().get("count"), int):
            error = "invalid count"
        elif route == "events" and not response.content.startswith(b"data: "):
            error = "invalid SSE snapshot"
        elif route == "dashboard" and "text/html" not in response.headers.get("content-type", ""):
            error = "invalid dashboard content type"
    except (httpx.HTTPError, ValueError, TypeError, AttributeError) as exc:
        error = type(exc).__name__
    return {
        "latency_ms": (time.perf_counter_ns() - start) / 1_000_000,
        "response_bytes": size,
        "status": status,
        "error": error,
    }


def run_benchmark(urls, pids, *, samples=100, rounds=3, warmup=20,
                  token="stack-benchmark-local", idle_seconds=0.5, startup=None, startup_notes=None):
    if samples < 1 or rounds < 1 or warmup < 0 or idle_seconds <= 0:
        raise ValueError("samples/rounds must be positive, warmup nonnegative and idle_seconds positive")
    if set(urls) != {"go", "django"} or set(pids) != {"go", "django"}:
        raise ValueError("Both go and django URLs and PIDs are required")
    urls = {name: url.rstrip("/") for name, url in urls.items()}
    order = [["go", "django"] if index % 2 == 0 else ["django", "go"] for index in range(rounds)]
    report = {
        "schema_version": 1,
        "recorded_at": datetime.now(timezone.utc).isoformat(),
        "scope": "local synthetic single-host sequential HTTP; no production or VPS qualification",
        "synthetic": True,
        "environment": {
            "os": platform.platform(), "machine": platform.machine(),
            "python": platform.python_version(), "logical_cpus": psutil.cpu_count(),
            "physical_cpus": psutil.cpu_count(logical=False),
            "host_memory_bytes": psutil.virtual_memory().total,
            "harness_pid": os.getpid(),
            "harness_dependencies": {name: importlib.metadata.version(name) for name in ("httpx", "psutil")},
        },
        "method": {
            "samples_per_route_per_round": samples, "rounds": rounds,
            "warmup_per_route_per_backend": warmup, "backend_order": order,
            "route_order": list(ROUTES), "concurrency": 1, "timeout_seconds": 5,
            "http_keepalive": True, "http2": False, "redirects": False,
            "percentiles": "nearest rank, all attempts including errors",
            "cpu": "process tree CPU time delta / wall time; one-core and machine-normalized percentages",
            "rss": "sum of process-tree RSS sampled every 20ms, not unique/private memory",
            "idle_window_seconds": idle_seconds,
        },
        "startup": startup or {"go": None, "django": None},
        "startup_notes": startup_notes,
        "cold_build_and_cache": {"measured": False, "reason": "build/cache conditions require a separate controlled run"},
        "processes": {}, "warmup": {}, "results": {},
    }
    for name in ("go", "django"):
        process = psutil.Process(pids[name])
        with ProcessWindow(pids[name]) as idle:
            time.sleep(idle_seconds)
        report["processes"][name] = {
            "pid": pids[name], "name": process.name(), "created_at_unix": process.create_time(),
            "url": urls[name], "idle": idle.result,
        }
        report["results"][name] = {route: {"rounds": []} for route in ROUTES}

    clients = {name: httpx.Client(
        headers={"Authorization": "Bearer " + token}, timeout=5,
        limits=httpx.Limits(max_connections=1, max_keepalive_connections=1),
        trust_env=False, follow_redirects=False, http2=False,
    ) for name in urls}
    try:
        for name in ("go", "django"):
            report["warmup"][name] = {}
            for route in ROUTES:
                rows = [request_sample(clients[name], urls[name], route) for _ in range(warmup)]
                report["warmup"][name][route] = {"summary": summarize(rows), "samples": rows}
        for index, names in enumerate(order, start=1):
            for name in names:
                for route in ROUTES:
                    with ProcessWindow(pids[name]) as load:
                        rows = [request_sample(clients[name], urls[name], route) for _ in range(samples)]
                    report["results"][name][route]["rounds"].append({
                        "round": index, "samples": rows, "summary": summarize(rows), "process": load.result,
                    })
    finally:
        for client in clients.values():
            client.close()
    for routes in report["results"].values():
        for result in routes.values():
            result["summary"] = summarize([sample for item in result["rounds"] for sample in item["samples"]])
    return report


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go-url", default="http://127.0.0.1:18480")
    parser.add_argument("--django-url", default="http://127.0.0.1:18481")
    parser.add_argument("--go-pid", type=int, required=True)
    parser.add_argument("--django-pid", type=int, required=True)
    parser.add_argument("--samples", type=int, default=100)
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--go-startup-ms", type=float)
    parser.add_argument("--django-startup-ms", type=float)
    parser.add_argument("--startup-notes", help="Describe timing method and cold/warm cache conditions for supplied startup durations")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    startup = {}
    for name, duration in (("go", args.go_startup_ms), ("django", args.django_startup_ms)):
        if duration is not None and (duration < 0 or not math.isfinite(duration)):
            parser.error("startup durations must be finite and nonnegative")
        startup[name] = {"duration_ms": duration, "source": "explicit CLI input; not measured by harness"} if duration is not None else None
    report = run_benchmark(
        {"go": args.go_url, "django": args.django_url},
        {"go": args.go_pid, "django": args.django_pid},
        samples=args.samples, rounds=args.rounds,
        token=os.environ.get("BENCH_TOKEN", "stack-benchmark-local"),
        startup=startup, startup_notes=args.startup_notes,
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    errors = sum(value["summary"]["error_count"] for routes in report["results"].values() for value in routes.values())
    warmup_errors = sum(value["summary"]["error_count"] for routes in report["warmup"].values() for value in routes.values())
    print(json.dumps({"output": str(args.output.resolve()), "errors": errors, "warmup_errors": warmup_errors}))
    return 1 if errors or warmup_errors else 0


if __name__ == "__main__":
    sys.exit(main())
