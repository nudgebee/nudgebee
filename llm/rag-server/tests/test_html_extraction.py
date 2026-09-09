"""Tests for how ingested HTML becomes indexed text.

A lossy extractor has no failure mode: pages still index, searches still return
hits, answers still read well — they are just built on a document whose commands
and tables were deleted at ingest. Every source grew its own tag whitelist, and
each whitelist quietly decided which parts of a document an agent would never
see: Confluence dropped every code block, the product docs dropped every table.
These tests pin what used to disappear.

The Confluence fixture is the rendered body of a real runbook page
(``SOP-Nudgebee-EventBridge-SQS-Queue-Backlog``) — a synthetic snippet would not
have caught the original bug, because the bug was about how Confluence represents
its own macros.
"""

import re
from pathlib import Path

from bs4 import BeautifulSoup

from rag.core.documents.scraper import _extract_nudgebee_doc_content, extract_content, page_body_html

FIXTURE = Path(__file__).parent / "fixtures" / "confluence_sop_page_view.html"


def rendered_page():
    return extract_content(FIXTURE.read_text())


def fenced_blocks(markdown):
    return re.findall(r"```.*?\n(.*?)```", markdown, re.S)


def test_page_body_reads_the_rendered_view():
    # Storage format hides code inside <ac:structured-macro> CDATA that no HTML
    # parse can reach, so reading it back would silently reintroduce the bug.
    assert page_body_html({"body": {"view": {"value": "<p>rendered</p>"}}}) == "<p>rendered</p>"
    assert page_body_html({"body": {"storage": {"value": "<p>raw</p>"}}}) is None
    assert page_body_html({}) is None
    # Not a dict: raising here would abort the rest of the sync run, not one page.
    assert page_body_html("<html>not json</html>") is None
    assert page_body_html(None) is None


def test_commands_survive_inside_fenced_blocks():
    blocks = fenced_blocks(rendered_page())
    assert len(blocks) >= 7, "runbook code blocks were dropped"

    commands = "\n".join(blocks)
    for command in (
        "kubectl -n nudgebee get pods -l app=cloud-collector-server",
        "kubectl -n nudgebee top pods -l app=cloud-collector-server",
        "kubectl -n nudgebee logs -l app=cloud-collector-server",
    ):
        assert command in commands, f"{command!r} missing from the extracted code blocks"


def test_tables_keep_their_rows():
    # A decision matrix flattened to one cell per line is present but unusable:
    # the symptom loses its diagnosis. Assert both cells stay on one row.
    markdown = rendered_page()
    row = next((line for line in markdown.splitlines() if "CrashLoopBackOff" in line and line.startswith("|")), None)
    assert row is not None, "table row containing CrashLoopBackOff was not emitted as a table"
    assert "crash-looping" in row, "symptom and diagnosis were split across rows"


def test_identifiers_stay_searchable_tokens():
    # get_text(strip=True) with no separator fused inline <code> into the prose
    # around it ("theApproximateAgeOfOldestMessagemetric"), so the identifier a
    # user searches for did not exist in the indexed text.
    markdown = rendered_page()
    for identifier in ("ApproximateAgeOfOldestMessage", "nudgebee-eventbridge-queue"):
        pattern = r"(?<![A-Za-z0-9_-])" + re.escape(identifier) + r"(?![A-Za-z0-9_-])"
        assert re.search(pattern, markdown), f"{identifier!r} is not a standalone token"


def test_subheadings_are_preserved():
    markdown = rendered_page()
    assert "### Step 1: Check cloud-collector pod health" in markdown
    assert len(re.findall(r"^### ", markdown, re.M)) >= 10, "h3 headings were dropped"


def test_prose_is_not_duplicated():
    # Nested <p> inside <li>/<td> matched find_all twice, so a quarter of the
    # indexed lines were repeats eating the retrieval budget. Code blocks are
    # excluded: a runbook legitimately repeats the same command across steps.
    prose = re.sub(r"```.*?```", "", rendered_page(), flags=re.S)
    lines = [line for line in prose.splitlines() if len(line.strip()) > 60]
    repeated = {line for line in lines if lines.count(line) > 1}
    assert not repeated, f"prose emitted more than once: {sorted(repeated)[:3]}"


def test_same_page_anchors_keep_their_text_but_lose_the_link():
    # A page is one document, so a link to its own heading points at text that
    # is already being embedded. External links keep their target.
    markdown = extract_content(
        '<p><a href="#architecture">Architecture</a> and <a href="https://x.test/d">docs</a></p>'
    )
    assert "Architecture" in markdown
    assert "(#architecture)" not in markdown
    assert "(https://x.test/d)" in markdown


def test_identifiers_in_plain_prose_are_not_markdown_escaped():
    # Only identifiers the author wrapped in <code> survive escaping by default.
    # One written as ordinary prose becomes "cloud\\_collector\\_..." and stops
    # matching what a user searches for.
    markdown = extract_content("<p>Set cloud_collector_aws_eventbridge_sqs before the 5.0 * 2 window.</p>")
    assert "cloud_collector_aws_eventbridge_sqs" in markdown
    assert "\\" not in markdown


FURNITURE_HTML = (
    "<nav>nav junk</nav><header>header junk</header><p>keep me</p>"
    "<aside>IMPORTANT rotate the key first</aside><footer>footer junk</footer>"
    "<script>window.x = 1</script><style>.a{color:red}</style>"
)


def test_scripts_and_styles_never_reach_the_index():
    # markdownify's `strip=` option is the wrong tool here — it drops the tag and
    # keeps the text, which would push script bodies into the index. Only
    # removing the node removes the content.
    markdown = extract_content(FURNITURE_HTML)
    assert "keep me" in markdown
    assert "window.x" not in markdown
    assert "color:red" not in markdown


def test_a_content_fragment_keeps_its_asides():
    # Confluence hands us a body fragment, not a page: there is no site chrome to
    # strip, so an <aside> is something the author wrote. Deleting it would be
    # the same silent loss this module exists to stop.
    markdown = extract_content(FURNITURE_HTML)
    assert "IMPORTANT rotate the key first" in markdown


def test_a_whole_page_drops_its_site_chrome():
    # A caller handed a rendered page opts in, and there nav/footer really are
    # the site's furniture repeated on every page.
    content, _ = _extract_nudgebee_doc_content(f"<html><body><article>{FURNITURE_HTML}</article></body></html>")
    assert "keep me" in content
    for junk in ("nav junk", "header junk", "footer junk", "IMPORTANT rotate the key first"):
        assert junk not in content


# --- Nudgebee product docs -------------------------------------------------
#
# The docs scraper had the same tag-whitelist shape as the Confluence one and
# lost tables outright: Docusaurus renders a markdown table as bare <td>, so
# unlike Confluence there is no nested <p> for a whitelist to catch by accident.
# 74 of 263 docs pages carry a table, and they hold the config keys and defaults
# that product questions are actually about.

DOCS_PAGE = """<html><head><title>Configuring the collector | NudgeBee</title></head><body>
<nav>Docs Blog</nav>
<article>
<h2>Configuration</h2>
<table><thead><tr><th>Key</th><th>Default</th></tr></thead>
<tbody><tr><td>maxRetries</td><td>5</td></tr><tr><td>timeoutSeconds</td><td>30</td></tr></tbody></table>
<h4>Advanced</h4>
<blockquote>Applies per tenant.</blockquote>
<pre><code>helm upgrade nudgebee --set collector.maxRetries=10</code></pre>
<p>Restart the collector afterwards.</p>
</article>
<footer>Copyright</footer></body></html>"""


def test_docs_page_keeps_its_configuration_table():
    content, title = _extract_nudgebee_doc_content(DOCS_PAGE)
    assert title == "Configuring the collector | NudgeBee"
    row = next((line for line in content.splitlines() if "maxRetries" in line), None)
    assert row is not None, "configuration table was dropped"
    assert "5" in row, "the key survived but its default did not"
    assert "timeoutSeconds" in content


def test_docs_page_keeps_deep_headings_quotes_and_commands():
    content, _ = _extract_nudgebee_doc_content(DOCS_PAGE)
    assert "#### Advanced" in content, "h4 headings were dropped"
    assert "Applies per tenant." in content, "blockquote was dropped"
    assert "helm upgrade nudgebee --set collector.maxRetries=10" in content
    assert "Restart the collector afterwards." in content


def test_docs_page_drops_site_furniture():
    content, _ = _extract_nudgebee_doc_content(DOCS_PAGE)
    assert "Docs Blog" not in content
    assert "Copyright" not in content


# --- nothing is silently lost ----------------------------------------------


def visible_tokens(text):
    return {t.lower() for t in re.findall(r"[A-Za-z0-9_.:/-]{4,}", text)}


def source_tokens(html, scoped_to_article=False):
    """Every word visible on the page, minus the furniture we remove on purpose."""
    soup = BeautifulSoup(html, "html.parser")
    root = (soup.find("article") or soup) if scoped_to_article else soup
    for tag in root(["script", "style", "nav", "header", "footer", "aside"]):
        tag.decompose()
    return visible_tokens(root.get_text(" ", strip=True))


def test_confluence_conversion_loses_no_visible_text():
    # The guard the original bug needed. Pinning individual features (code
    # blocks, tables, h3s) only catches losses someone already thought of;
    # this catches any word that stops reaching the index, whatever the cause.
    html = FIXTURE.read_text()
    missing = source_tokens(html) - visible_tokens(extract_content(html))
    assert not missing, f"text present on the page never reached the index: {sorted(missing)[:10]}"


def test_docs_conversion_loses_no_visible_text():
    content, _ = _extract_nudgebee_doc_content(DOCS_PAGE)
    missing = source_tokens(DOCS_PAGE, scoped_to_article=True) - visible_tokens(content)
    assert not missing, f"text present on the page never reached the index: {sorted(missing)[:10]}"


def test_fenced_blocks_are_left_byte_for_byte_alone():
    # Blank lines inside a fence are content — an ASCII diagram or a heredoc
    # means something different once it has been reflowed.
    markdown = extract_content("<p>before</p><pre><code>line one\n\n\n\nline two</code></pre><p>after</p>")
    assert "line one\n\n\n\nline two" in markdown
    # ...while prose carrying no fence still collapses.
    assert "\n\n\n" not in extract_content("<p>one</p><br/><br/><br/><br/><p>two</p>")


def test_images_keep_their_alt_text_and_lose_their_url():
    # Confluence attachment and emoticon URLs are relative, so nothing
    # downstream can resolve them: they are tokens spent on a dead link.
    markdown = extract_content(
        '<p>See <img src="/download/attachments/1/topology.png" alt="architecture diagram"> '
        'and <img src="/images/icons/emoticons/check.png" alt=""></p>'
    )
    assert "architecture diagram" in markdown
    assert "topology.png" not in markdown
    assert "emoticons" not in markdown
    assert "](" not in markdown
