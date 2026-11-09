"""document_key must produce exactly what llm-server's DocumentKey does, pinned by the shared vectors."""

import json
from pathlib import Path

import pytest

from rag.core.documents.collection import SOURCE_ID_KEYS as COLLECTION_SOURCE_ID_KEYS
from rag.core.documents.document_key import SOURCE_ID_KEYS, document_key, label_key

FIXTURE = Path(__file__).resolve().parents[3] / "docs" / "fixtures" / "kb_document_keys.json"
CASES = json.loads(FIXTURE.read_text())["cases"]


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_matches_golden_vector(case):
    assert document_key(case["collection"], case["metadata"], case["url"]) == case["key"]


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_label_key_matches_golden_vector(case):
    assert label_key(case["collection"], case["metadata"], case["url"]) == case["label_key"]


def test_source_id_keys_stay_in_step_with_the_collection_module():
    assert SOURCE_ID_KEYS == COLLECTION_SOURCE_ID_KEYS


def test_whole_float_renders_like_llm_server():
    assert document_key("int_knowledge_base", {"page_id": 12345.0}) == "int_knowledge_base|12345"
