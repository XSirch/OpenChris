"""Gera o Compose que pode ser colado diretamente no EasyPanel."""

import base64
import gzip
import io
import tarfile
import textwrap
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "dist" / "openchris" / "compose.yaml"
BINARY_ASSETS = {".png", ".woff2"}


def source_archive():
    archive = io.BytesIO()
    patterns = ("*.py", "templates/*.html", "static/*.css", "static/*.js", "static/*.svg",
                "static/*.txt", "static/*.png", "static/*.woff2")
    files = sorted(path for pattern in patterns for path in (ROOT / "openchris").glob(pattern))
    with tarfile.open(fileobj=archive, mode="w") as tar:
        for path in files:
            if path.is_symlink() or not path.is_file():
                raise ValueError(f"Arquivo de origem inválido: {path}")
            data = path.read_bytes() if path.suffix in BINARY_ASSETS else path.read_text(encoding="utf-8").encode("utf-8")
            entry = tarfile.TarInfo(path.relative_to(ROOT).as_posix())
            entry.size = len(data)
            tar.addfile(entry, io.BytesIO(data))
    with io.BytesIO() as compressed:
        with gzip.GzipFile(fileobj=compressed, mode="wb", mtime=0) as stream:
            stream.write(archive.getvalue())
        return base64.b64encode(compressed.getvalue()).decode("ascii")


def render():
    recipe = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    requirements = (ROOT / "requirements.txt").read_text(encoding="utf-8")
    copies = {
        "COPY requirements.txt .": "COPY <<'REQUIREMENTS' requirements.txt\n" + requirements.rstrip("\n") + "\nREQUIREMENTS",
        "COPY openchris/ openchris/": "\n".join((
            "COPY <<'OPENCHRIS_SOURCE' /tmp/openchris-source.b64",
            textwrap.fill(source_archive(), width=96),
            "OPENCHRIS_SOURCE",
            "RUN python - <<'PYTHON'",
            "import base64, io, tarfile",
            "from pathlib import Path",
            "source = Path('/tmp/openchris-source.b64')",
            "with tarfile.open(fileobj=io.BytesIO(base64.b64decode(source.read_bytes())), mode='r:gz') as archive:",
            "    archive.extractall('.', filter='data')",
            "source.unlink()",
            "PYTHON",
        )),
    }
    for instruction, inline in copies.items():
        if recipe.count(instruction) != 1:
            raise ValueError(f"Revise o empacotamento para {instruction}")
        recipe = recipe.replace(instruction, inline)
    config = (ROOT / "compose.yaml").read_text(encoding="utf-8")
    if config.count("      context: .\n") != 2:
        raise ValueError("Revise os serviços construídos pelo Compose.")
    builds = config.replace("      context: .\n", "      <<: *openchris-build\n")
    header = "# OpenChris para EasyPanel. Gerado por tools/export_compose.py.\n"
    header += "x-build: &openchris-build\n  context: .\n  dockerfile_inline: |\n"
    return header + textwrap.indent(recipe.replace("$", "$$"), "    ") + "\n" + builds


if __name__ == "__main__":
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    OUTPUT.write_text(render(), encoding="utf-8", newline="\n")
