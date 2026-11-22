"""Extract a bounded text index in a separate process. PDF bytes stay local."""

import io
import json
import logging
import sys

from pypdf import PdfReader
from pypdf.errors import PdfReadError

INDEX_CHARS = 150_000
PDF_PAGES = 120


def extract_pdf(source):
    try:
        reader = PdfReader(io.BytesIO(source))
        if reader.is_encrypted:
            return "locked", []
        count = len(reader.pages)
    except (PdfReadError, ValueError, KeyError, TypeError, OSError):
        return "invalid", []
    pages, remaining = [], INDEX_CHARS
    partial = count > PDF_PAGES
    for index in range(min(count, PDF_PAGES)):
        try:
            page = reader.pages[index]
            value = (page.extract_text(extraction_mode="layout", layout_mode_space_vertically=False) or "").strip()
        except (PdfReadError, ValueError, KeyError, TypeError, OSError, NotImplementedError):
            partial = True
            continue
        if len(value) > remaining:
            value, partial = value[:remaining], True
        remaining -= len(value)
        if any(char.isalnum() for char in value):
            pages.append([index + 1, value])
        if not remaining:
            partial = partial or index + 1 < count
            break
    if not pages:
        return ("unreadable" if partial else "no_text"), []
    return ("partial" if partial else "ready"), pages


if __name__ == "__main__":
    logging.disable(logging.CRITICAL)
    if sys.platform.startswith("linux"):
        import resource

        memory_limit = 320 * 1024 * 1024
        resource.setrlimit(resource.RLIMIT_AS, (memory_limit, memory_limit))
        resource.setrlimit(resource.RLIMIT_CPU, (9, 10))
    print(json.dumps(extract_pdf(sys.stdin.buffer.read()), ensure_ascii=True))
