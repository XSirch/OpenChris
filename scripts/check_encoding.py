"""Check project text as strict UTF-8; report exact bytes before any correction."""
from pathlib import Path
import json
import subprocess
import sys


def main():
    root = Path(__file__).resolve().parents[1]
    paths = subprocess.check_output(
        ["git", "ls-files", "-c", "-o", "--exclude-standard", "-z"], cwd=root
    ).decode("utf-8").split("\0")
    suffixes = {".go", ".py", ".ps1", ".js", ".cjs", ".html", ".css", ".sql", ".json", ".md", ".yaml", ".yml", ".txt", ".sum", ".mod", ".example"}
    names = {"Dockerfile", ".gitignore", ".dockerignore", ".gitattributes"}
    checked, errors = 0, []
    for relative in sorted(set(paths) - {""}):
        path = root / relative
        if path.suffix not in suffixes and path.name not in names:
            continue
        data = path.read_bytes()
        checked += 1
        try:
            data.decode("utf-8", errors="strict")
        except UnicodeDecodeError as error:
            errors.append({
                "file": relative,
                "line": data[:error.start].count(b"\n") + 1,
                "offset": error.start,
                "problem_bytes_hex": data[error.start:error.end].hex(" "),
            })
    print(json.dumps({"checked_text_files": checked, "invalid_utf8": errors}, ensure_ascii=False))
    return bool(errors)


if __name__ == "__main__":
    sys.exit(main())
