"""Tests that generic Slack notifications (message type "generic", e.g. the
message runbook-server's notifications.im task sends via /api/messages/send)
render GFM tables and Mermaid diagrams as native Slack blocks instead of
degrading to plain joined text - the same rendering events.py's
handle_final_response uses for LLM investigation replies.
"""

from notifications_server.message_templates.slack.generic import (
    MAX_BLOCKS,
    GenericMessageParams,
    get_slack_generic_message_template,
)

TABLE_TEXT = """| Resource | Current ($) | Previous ($) |
|---|---|---|
| EC2 | 120 | 100 |
| RDS | 80 | 90 |
"""

PIE_MERMAID = """```mermaid
pie title "Resource Usage"
    "CPU" : 45
    "Memory" : 30
    "Disk" : 25
```"""

FLOWCHART_MERMAID = """```mermaid
graph TD
    S1["API Gateway"] --> S2["Auth Service"]
```"""

# Same fixture shape as tests/test_nb_chart.py - the FinOps agent's own
# ```nb-chart JSON convention (unrelated to Mermaid).
NB_CHART_PIE = (
    '{"type":"pie","title":"Cost by Service","labels":["EC2","RDS","S3"],"values":[1200,800,150],"format":"usd"}'
)


def _report_with_n_small_tables(n):
    return "\n".join(f"Namespace ns-{i}:\n\n| Metric | Value |\n|---|---|\n| cost | {i} |\n" for i in range(n))


def _blocks(message, **kwargs):
    return get_slack_generic_message_template(GenericMessageParams(message=message, **kwargs))["blocks"]


class TestPlainMessageUnchanged:
    def test_plain_message_still_renders_as_sections(self):
        blocks = _blocks("**bold** and a normal line\n# Header")
        assert all(b.get("type") == "section" for b in blocks)

    def test_empty_message_renders_no_body_blocks(self):
        assert _blocks("") == []


class TestTableRendersAsNativeBlock:
    def test_gfm_table_becomes_a_table_block(self):
        blocks = _blocks(f"Here is the data:\n\n{TABLE_TEXT}\nEnd of report.")
        block_types = [b["type"] for b in blocks]
        assert "table" in block_types
        assert block_types.count("section") == 2  # "Here is the data:" and "End of report."

    def test_non_table_pipe_text_falls_back_to_plain_section(self):
        blocks = _blocks("just | some | text | without a separator row")
        assert all(b.get("type") == "section" for b in blocks)


class TestMermaidRendersAsNativeBlock:
    def test_pie_chart_becomes_a_data_visualization_block(self):
        blocks = _blocks(f"Breakdown:\n\n{PIE_MERMAID}")
        block_types = [b["type"] for b in blocks]
        assert "data_visualization" in block_types

    def test_flowchart_falls_back_to_labeled_code_block(self):
        # Only xychart/pie render as native charts; other diagram types fall
        # back to a labeled fenced code block (Slack can't render arbitrary
        # diagrams inline) instead of leaving raw ```mermaid fences in a
        # plain section, which would render as unreadable literal text.
        blocks = _blocks(FLOWCHART_MERMAID)
        context_texts = [el["text"] for b in blocks if b["type"] == "context" for el in b.get("elements", [])]
        assert any("can't render diagrams inline" in t for t in context_texts)
        assert any("graph TD" in b.get("text", {}).get("text", "") for b in blocks if b["type"] == "section")


class TestNbChartRendersAsNativeBlock:
    def test_nb_chart_fence_becomes_a_data_visualization_block(self):
        blocks = _blocks(f"Cost breakdown:\n\n```nb-chart\n{NB_CHART_PIE}\n```")
        block_types = [b["type"] for b in blocks]
        assert "data_visualization" in block_types


class TestBlockBudgetOverflow:
    def test_table_heavy_message_silently_truncates_past_max_blocks(self):
        # Known, deliberately-accepted limitation (tracked in
        # nudgebee-enterprise#36313): rich rendering spends the block budget
        # per element - each table costs ~1-2 blocks regardless of how little
        # text it holds - so a report built from many small tables can hit
        # the 50-block cap and silently lose trailing content, even though
        # the same message easily fit as one section block under the old
        # plain-text-only rendering. This pins the current (accepted)
        # behavior so a future change to it is a conscious decision, not an
        # accident.
        message = _report_with_n_small_tables(30)
        blocks = _blocks(message)

        assert len(blocks) == MAX_BLOCKS
        all_text = "\n".join(b["text"]["text"] for b in blocks if b.get("type") == "section" and "text" in b)
        missing = [i for i in range(30) if f"ns-{i}" not in all_text]
        assert missing, "expected some namespaces to be silently dropped past the block cap"


class TestApprovalMessagesUnaffected:
    def test_approval_message_with_table_keeps_old_blockquote_behavior(self):
        # Approval prompts stay on the plain-quoted path: Slack blockquote
        # (`> ` per line) doesn't apply to table/chart blocks, and approval
        # prompts are short text, not table/diagram-bearing.
        blocks = _blocks(
            f"Approve this?\n\n{TABLE_TEXT}",
            approval_token="tok-1",
            approval_options=["approve", "reject"],
        )
        body_sections = [b for b in blocks if b.get("type") == "section" and "text" in b]
        assert all(b["text"]["text"].startswith("> ") for b in body_sections)
        assert not any(b["type"] == "table" for b in blocks)
