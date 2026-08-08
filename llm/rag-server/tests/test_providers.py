from rag.core.llm.providers import _ensure_user_content


def test_ensure_user_content_keeps_real_prompts():
    assert _ensure_user_content("rank these docs") == "rank these docs"
    assert _ensure_user_content("  hi  ") == "  hi  "  # inner content preserved verbatim


def test_ensure_user_content_falls_back_when_empty():
    # An empty / whitespace-only user turn trips Qwen's "No user query found" template error.
    assert _ensure_user_content("") == "Continue."
    assert _ensure_user_content("   ") == "Continue."
    assert _ensure_user_content("\n\t ") == "Continue."
