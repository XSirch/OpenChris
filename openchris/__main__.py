import logging
import sys
import time


def worker():
    from .automation import tick
    from .banking import sync_due
    from .db import initialize

    initialize()
    log = logging.getLogger("openchris.worker")
    while True:
        for task in (sync_due, tick):
            try:
                task()
            except Exception as exc:
                # Some driver exceptions include raw bank data or credentials in their details.
                log.error("Falha em %s (%s). Nova tentativa no próximo ciclo.", task.__name__, type(exc).__name__)
        time.sleep(30)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    command = sys.argv[1] if len(sys.argv) > 1 else "web"
    if command == "worker":
        worker()
    elif command == "web":
        from waitress import serve
        from .web import create_app

        serve(create_app(), host="0.0.0.0", port=8000, threads=6)
    else:
        raise SystemExit("Use web ou worker.")
