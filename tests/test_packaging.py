import base64
import io
import tarfile

from tools.export_compose import OUTPUT, ROOT, render, source_archive


def test_easypanel_release_contains_current_application():
    assert OUTPUT.read_text(encoding="utf-8") == render()
    files = {
        path.relative_to(ROOT).as_posix(): path.read_text(encoding="utf-8").encode("utf-8")
        for path in (ROOT / "openchris").rglob("*")
        if path.is_file() and "__pycache__" not in path.parts
    }
    with tarfile.open(fileobj=io.BytesIO(base64.b64decode(source_archive())), mode="r:gz") as tar:
        assert {item.name: tar.extractfile(item).read() for item in tar} == files
