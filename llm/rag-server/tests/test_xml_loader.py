import os

os.environ.setdefault("APP_DATABASE_URL", "postgresql://u:p@localhost:5432/db")
os.environ.setdefault("RELAY_SERVER_SECRET_KEY", "test")

from rag.core.documents.loaders.file_loaders import UnstructuredXMLLoader  # noqa: E402


def test_xml_loader_returns_text_nodes_without_tags_or_comments(tmp_path):
    path = tmp_path / "doc.xml"
    path.write_text(
        "<?xml version='1.0'?><root><!-- hidden --><a>first</a><b> second <c>third</c></b></root>",
        encoding="utf-8",
    )

    docs = UnstructuredXMLLoader(str(path)).load()

    assert len(docs) == 1
    assert docs[0].page_content == "first\nsecond third"
    assert docs[0].metadata == {"source": str(path)}


def test_xml_loader_keeps_inline_text_together_and_separates_siblings(tmp_path):
    path = tmp_path / "inline.xml"
    path.write_text(
        "<?xml version='1.0'?><root><p>This is <b>bold</b> text.</p><a>1</a><a>2</a></root>",
        encoding="utf-8",
    )

    docs = UnstructuredXMLLoader(str(path)).load()

    assert docs[0].page_content == "This is bold text.\n1\n2"


def test_xml_loader_does_not_resolve_external_entities(tmp_path):
    secret = tmp_path / "secret.txt"
    secret.write_text("TOPSECRET", encoding="utf-8")
    path = tmp_path / "xxe.xml"
    path.write_text(
        f"<?xml version='1.0'?><!DOCTYPE r [<!ENTITY x SYSTEM 'file://{secret}'>]><r>&x;</r>",
        encoding="utf-8",
    )

    docs = UnstructuredXMLLoader(str(path)).load()

    assert "TOPSECRET" not in docs[0].page_content


def test_pysnow_imports_with_oauthlib_4():
    import oauthlib  # type: ignore[import-untyped]
    import pysnow  # type: ignore[import-untyped]  # noqa: F401

    assert int(oauthlib.__version__.split(".")[0]) >= 4
