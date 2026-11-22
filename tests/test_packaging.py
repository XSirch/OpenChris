import base64
import io
import re
import tarfile
from urllib.parse import parse_qs, urlsplit

from tools.export_compose import OUTPUT, ROOT, render, source_archive


def test_easypanel_release_contains_current_application():
    assert OUTPUT.read_text(encoding="utf-8") == render()
    files = {
        path.relative_to(ROOT).as_posix(): (
            path.read_bytes() if path.suffix in {".png", ".woff2"} else path.read_text(encoding="utf-8").encode("utf-8")
        )
        for path in (ROOT / "openchris").rglob("*")
        if path.is_file() and "__pycache__" not in path.parts
    }
    with tarfile.open(fileobj=io.BytesIO(base64.b64decode(source_archive())), mode="r:gz") as tar:
        assert {item.name: tar.extractfile(item).read() for item in tar} == files


def test_easypanel_inline_build_does_not_exceed_linux_exec_argument_limit():
    compose = render()
    heredocs = re.finditer(
        r"(?ms)^    RUN [^\n]*<<'?(?P<delimiter>[A-Za-z_][A-Za-z_0-9]*)'?\s*\n"
        r"(?P<body>.*?)^    (?P=delimiter)\s*$",
        compose,
    )
    for heredoc in heredocs:
        assert len(heredoc.group(0).encode("utf-8")) < 120_000, (
            "An inline RUN instruction exceeds the safe Linux exec argument size"
        )


def test_frontend_assets_share_a_cache_revision():
    templates = ROOT / "openchris" / "templates"
    static = ROOT / "openchris" / "static"
    index = (templates / "index.html").read_text(encoding="utf-8")
    revision = re.search(r"filename='app\.js',\s*v='([^']+)'", index).group(1)

    for template in (templates / "index.html", templates / "login.html"):
        content = template.read_text(encoding="utf-8")
        for filename in ("site.css", "marca.svg"):
            assert re.search(rf"filename='{re.escape(filename)}',\s*v='{re.escape(revision)}'", content)

    for script in static.glob("*.js"):
        content = script.read_text(encoding="utf-8")
        for imported in re.findall(r"\bfrom\s+['\"](\./[^'\"]+)['\"]", content):
            url = urlsplit(imported)
            assert parse_qs(url.query) == {"v": [revision]}, (script.name, imported)
            assert (static / url.path).is_file(), (script.name, imported)
