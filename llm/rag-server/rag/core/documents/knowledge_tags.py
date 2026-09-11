"""Add manual KB labels to the searchable representation, not its stored source."""

import json

from rag.core.types import Document


def add_knowledge_context_tags(documents, tags):
    # Match llm-server validation, including callers using the RAG API directly.
    if len(tags) > 32:
        raise ValueError("At most 32 knowledge tags are allowed")
    normalized = []
    seen = set()
    for tag in tags:
        tag = tag.strip()
        if not tag:
            continue
        if len(tag) > 128 or any(ord(c) < 32 or 127 <= ord(c) <= 159 for c in tag):
            raise ValueError("Invalid knowledge tag")
        if tag.lower() not in seen:
            normalized.append(tag)
            seen.add(tag.lower())
    if not normalized:
        return documents
    header = "Context tags: " + json.dumps(normalized, ensure_ascii=False) + "\n\n"
    return [
        Document(page_content=header + doc.page_content, metadata={**doc.metadata, "context_tags": normalized})
        for doc in documents
    ]
