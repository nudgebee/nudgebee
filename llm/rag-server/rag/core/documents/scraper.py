import logging
import os
import re
import time
import xml.etree.ElementTree as ET
from typing import List
from urllib.parse import urlparse

import pysnow  # type: ignore[import-untyped]
import requests
from atlassian import Confluence
from bs4 import BeautifulSoup, NavigableString
from filelock import FileLock, Timeout
from markdownify import MarkdownConverter  # type: ignore[import-untyped]
from rag.core.documents.processing import (
    handle_updated_documents,
    process_documents,
    update_integration_kb_load_result,
)
from rag.core.embeddings.generator import get_embeddings
from rag.core.types import Document
from rag.core.utils.db_query import (
    get_active_accounts_for_tenant,
    get_confluence_integrations_for_tenant,
    get_servicenow_kb_integrations_for_tenant,
    list_active_tenants,
)

from utils.config import Config
from utils.shared import get_collection_name

logger = logging.getLogger(__name__)

# Per-integration scrape locks: a periodic sync and an on-demand retrigger must
# never scrape the same integration concurrently.
lock_dir = "./.locks"
os.makedirs(lock_dir, exist_ok=True)

# Confluence auth types, mirroring the api-server integration schema
# (api-server/services/integrations/confluence.go).
CONFLUENCE_AUTH_CLOUD_API_TOKEN = "cloud_api_token"
CONFLUENCE_AUTH_DATACENTER_PAT = "datacenter_pat"
CONFLUENCE_AUTH_DATACENTER_PASSWORD = "datacenter_password"


def confluence_auth_type(value):
    """Normalise a stored auth_type.

    Integrations created before Data Center support have no auth_type at all, so
    an empty value must resolve to Cloud + Basic — what they already do.
    """
    mode = (value or "").strip()
    if mode in (CONFLUENCE_AUTH_DATACENTER_PAT, CONFLUENCE_AUTH_DATACENTER_PASSWORD):
        return mode
    return CONFLUENCE_AUTH_CLOUD_API_TOKEN


def build_confluence_client(config):
    """Construct a Confluence client for the integration's auth mode.

    Data Center personal access tokens authenticate as bearer tokens, which the
    library sends when given ``token=``; every other mode is HTTP Basic. Cloud
    serves its API under ``/wiki`` while Data Center serves it beneath whatever
    context path the instance uses, so the URL is normalised here rather than
    left to the library's hostname sniff — that sniff misses Cloud sites on
    custom domains.
    """
    host = (config.get("host") or "").strip().rstrip("/")
    mode = confluence_auth_type(config.get("auth_type"))

    if mode == CONFLUENCE_AUTH_CLOUD_API_TOKEN and not host.endswith("/wiki"):
        host = f"{host}/wiki"

    if mode == CONFLUENCE_AUTH_DATACENTER_PAT:
        return Confluence(url=host, token=config.get("token"), cloud=False)
    return Confluence(
        url=host,
        username=config.get("username"),
        password=config.get("token"),
        cloud=(mode == CONFLUENCE_AUTH_CLOUD_API_TOKEN),
    )


def fetch_all_pages(confluence, space_key):
    try:
        page_list = []
        limit = 50
        start = 0

        while True:
            pages = confluence.get_all_pages_from_space(space_key, start=start, limit=limit)
            if not pages:
                break

            page_list.extend(pages)
            start += limit

        return page_list
    except Exception as e:
        logger.warning(f"Failed to fetch pages in space {space_key}: {e}")
        return []


def fetch_page(confluence, p_id):
    """Fetch a page with its body and links, or None if it cannot be read.

    ``body.view`` — the server-rendered HTML — not ``body.storage``. Storage
    format wraps every code block in ``<ac:structured-macro><![CDATA[...]]>``,
    which no HTML parse surfaces as a ``pre``/``code`` element, so a storage-based
    extraction drops 100% of the commands in a runbook no matter which tags it
    asks for. View renders those macros to ordinary ``<pre>``. ``runbook_resolver``
    on the llm-server side already reads ``body.view`` for the same reason.
    """
    try:
        return confluence.get_page_by_id(p_id, expand="body.view")
    except Exception as e:
        logger.warning(f"Failed to fetch page {p_id}: {e}")
        return None


def page_body_html(page):
    # The isinstance guard is about blast radius, not tidiness: this runs outside
    # fetch_page's try, and the per-integration loop below has a finally but no
    # except. One page that comes back as something other than a dict would raise
    # here and take every remaining tenant in the sync run with it.
    if not isinstance(page, dict):
        return None
    body = page.get("body") or {}
    view = body.get("view") or {}
    return view.get("value")


class _IndexMarkdownConverter(MarkdownConverter):
    """The markdown flavour we index.

    Escaping off: this text is embedded and read by a model, never rendered by a
    markdown parser, so escaping only turns ``cloud_collector_aws_eventbridge_sqs``
    into ``cloud\\_collector\\_...`` and stops the identifier matching the term
    someone searches for.
    """

    class Options(MarkdownConverter.Options):
        heading_style = "ATX"
        escape_underscores = False
        escape_asterisks = False
        escape_misc = False

    def convert_img(self, el, text, parent_tags=None):
        # Alt text can carry meaning ("architecture diagram"); the src never
        # can. Confluence attachment and emoticon URLs are relative, so nothing
        # downstream resolves them — they are tokens spent on a dead link.
        return (el.attrs.get("alt") or "").strip()


_MARKDOWN = _IndexMarkdownConverter()


def collapse_blank_lines(text):
    """Collapse 3+ newlines to 2, leaving fenced blocks byte-for-byte alone.

    Whitespace inside a fence is content: an ASCII diagram or a heredoc means
    something different after it has been reflowed. Rewriting it would be the
    same silent mutation this module exists to stop, just smaller.
    """
    parts = re.split(r"(```.*?```)", text, flags=re.S)
    return "".join(p if p.startswith("```") else re.sub(r"\n{3,}", "\n\n", p) for p in parts)


def html_to_markdown(node, drop_page_furniture=False):
    """Convert a parsed HTML node to the markdown we index.

    Markdown rather than flat text because the structure IS content for the
    agents reading this: a fenced block marks a command as runnable, and a table
    that keeps its rows is a decision matrix instead of a column of loose cells.

    Shared by every HTML source we ingest. The bug this replaced came from each
    source growing its own tag whitelist, and each whitelist quietly deciding
    which parts of a document an agent would never see.

    ``drop_page_furniture`` is for callers handed a whole rendered page, where
    ``nav``/``footer``/``aside`` are the site's chrome. It stays off for callers
    handed a content fragment: there, an ``aside`` is a sidebar the author wrote
    ("IMPORTANT: rotate the key first"), and deleting it is exactly the silent
    loss this function replaced.
    """
    if drop_page_furniture:
        for tag in node(["nav", "header", "footer", "aside"]):
            tag.decompose()
    for tag in node(["script", "style"]):
        tag.decompose()
    # Keep the link text, drop same-page anchors. A page is indexed as one
    # document, so "[Architecture](#architecture)" points at itself: the target
    # is already in the text being embedded. Confluence puts one per heading,
    # which is 3% of the tokens on a typical runbook and buys nothing.
    for anchor in node.find_all("a", href=True):
        if anchor["href"].startswith("#"):
            anchor.unwrap()
    # Escaping off: this text is embedded and read by a model, never rendered by
    # a markdown parser, so the only thing escaping achieves is turning
    # ``cloud_collector_aws_eventbridge_sqs`` into ``cloud\_collector\_...`` —
    # which stops the identifier matching the term someone searches for. That is
    # the same class of defect as the concatenation this function replaced.
    text = _MARKDOWN.convert_soup(node)
    return collapse_blank_lines(text).strip()


def extract_content(content_html):
    """Convert a rendered Confluence page to markdown."""
    return html_to_markdown(BeautifulSoup(content_html, "html.parser"))


def confluence_page_url(confluence, page):
    """Build an absolute page URL from Confluence's own ``_links`` block.

    Confluence reports its base URL in every response, already correct for Cloud
    (where it includes ``/wiki``) and for Data Center served under a context
    path. Reconstructing it from the configured host gets Data Center wrong.
    """
    links = page.get("_links") or {}
    webui = links.get("webui") or ""
    if not webui:
        return ""
    base = (links.get("base") or "").rstrip("/")
    if not base:
        base = str(getattr(confluence, "url", "") or "").rstrip("/")
    return f"{base}{webui}"


def process_page_batch(page_queue, visited_pages, confluence, space_key, stats, tree_root=None):
    batch = []

    for _ in range(min(Config.embedding_batch_size, len(page_queue))):
        page_id = page_queue.pop(0)
        if page_id in visited_pages:
            continue

        logger.info(f"Processing Confluence page ID: {page_id}")
        visited_pages.add(page_id)

        page = fetch_page(confluence, page_id)
        if not page:
            stats["failed_pages"] += 1
            continue

        # Children before body: a container page with an empty body still has
        # a subtree beneath it, and in a page-tree scrape this walk is the only
        # thing that finds it.
        try:
            child_pages = confluence.get_child_pages(page_id)
        except Exception as e:
            logger.warning(f"Failed to list child pages of {page_id}: {e}")
            stats["failed_pages"] += 1
            child_pages = []
        for child in child_pages:
            child_id = child.get("id")
            if child_id and child_id not in visited_pages:
                page_queue.append(child_id)

        html_content = page_body_html(page)
        if html_content is None:
            # Counted, not just logged: body.view is rendered server-side, so
            # "the body did not render" (errored macro, Forge app, DC under
            # load) is a real new failure mode. Left uncounted it would sink the
            # page while the integration still reported a healthy sync.
            #
            # Absent, not empty. A container page whose body is "" is a normal
            # part of a page tree — its job is to hold children — and counting
            # those would report a failure on every tree that has one.
            logger.warning(f"Confluence page {page_id} returned no rendered body; not indexed")
            stats["failed_pages"] += 1
            continue
        if not html_content:
            continue

        content = extract_content(html_content)
        if content:
            page_url = confluence_page_url(confluence, page)
            title = (page.get("title") or "").strip()
            metadata = {"page_id": page_id, "url": page_url}
            if title:
                # The page title is the strongest retrieval token a runbook has
                # and body.view does not contain it — Confluence keeps it as a
                # separate field. Prepended and stamped, matching what the
                # ServiceNow and product-docs loaders already do.
                metadata["title"] = title
                content = f"Title: {title}\n\n{content}"
            if tree_root:
                metadata["tree_root"] = tree_root
            batch.append(Document(page_content=content, metadata=metadata))

    return batch


def collect_confluence_tree_documents(confluence, root_id, stats):
    """Walk one page tree — the root page and every page beneath it."""
    if not fetch_page(confluence, root_id):
        # A root that cannot be read is a permissions or configuration problem,
        # not an empty tree, and must not pass as a healthy sync.
        stats["failed_roots"] += 1
        return []
    visited_pages: set = set()
    page_queue = [root_id]
    documents: List[Document] = []
    while page_queue:
        documents.extend(process_page_batch(page_queue, visited_pages, confluence, None, stats, tree_root=root_id))
    logger.info(f"Collected {len(documents)} Confluence page documents under page {root_id}")
    return documents


def configured_page_trees(config):
    """Page IDs from the integration's ``page_trees`` value (comma-separated).

    api-server resolves pasted URLs to IDs at save, so only IDs arrive here.
    """
    return [p.strip() for p in (config.get("page_trees") or "").split(",") if p.strip()]


def collect_confluence_space_documents(confluence, space_key, stats):
    """Walk a Confluence space and return all page documents (no embedding).

    Embedding is done in a single process_documents call per integration so
    the load history records one row per sync, not one per page batch.
    """
    visited_pages: set = set()
    all_pages = fetch_all_pages(confluence, space_key)
    if not all_pages:
        # A space that yields no pages is either genuinely empty or unreadable
        # by these credentials. On-premise instances have heterogeneous space
        # permissions, so this is common and must not pass as a healthy sync.
        stats["empty_spaces"] += 1
    page_queue = [page["id"] for page in all_pages]
    documents: List[Document] = []
    while page_queue:
        documents.extend(process_page_batch(page_queue, visited_pages, confluence, space_key, stats))
    logging.info(f"Collected {len(documents)} Confluence page documents for space {space_key}")
    return documents


def fetch_all_spaces(confluence):
    """List every visible space key, following pagination.

    Enterprise Data Center instances routinely hold more spaces than a single
    page returns, and a truncated list silently indexes a subset.
    """
    space_keys: List[str] = []
    start = 0
    limit = 100
    while True:
        try:
            spaces = confluence.get_all_spaces(start=start, limit=limit)
        except Exception as e:
            logger.warning(f"Failed to fetch spaces at offset {start}: {e}")
            break
        results = spaces.get("results") if isinstance(spaces, dict) else None
        if not isinstance(results, list):
            logger.warning("Unexpected format for spaces")
            break
        space_keys.extend(space["key"] for space in results if space.get("key"))
        if len(results) < limit:
            break
        start += limit
    return space_keys


def _process_integration(integration, tenant_id, embeddings, trigger_type="system_sync", triggered_by="system"):
    """Scrape one Confluence integration and embed it as a single load.

    rag-server owns the integration KB's status flip — set to 'active' on
    completion (even with zero pages) or 'error' on failure.
    """
    config = integration["config"]
    integration_id = integration["integration_id"]
    collection_name = f"{integration_id}_knowledge_base"
    try:
        confluence = build_confluence_client(config)
        space_key = config.get("namespace")
        tree_roots = configured_page_trees(config)

        stats = {"failed_pages": 0, "empty_spaces": 0, "failed_roots": 0}
        documents: List[Document] = []
        space_keys: List[str] = []
        if tree_roots:
            # Page trees replace the space walk: the space (if any) only scoped
            # validation, and the trees are the whole of what gets indexed.
            for root_id in tree_roots:
                documents.extend(collect_confluence_tree_documents(confluence, root_id, stats))
        else:
            space_keys = [space_key] if space_key else fetch_all_spaces(confluence)
            for sk in space_keys:
                documents.extend(collect_confluence_space_documents(confluence, sk, stats))
        if stats["failed_pages"] or stats["empty_spaces"] or stats["failed_roots"]:
            logger.warning(
                "Confluence integration %s scraped with gaps: %s unreadable pages, "
                "%s of %s spaces returned nothing, %s of %s page trees unreadable",
                integration_id,
                stats["failed_pages"],
                stats["empty_spaces"],
                len(space_keys),
                stats["failed_roots"],
                len(tree_roots),
            )

        if stats["failed_roots"]:
            message = (
                f"{stats['failed_roots']} of {len(tree_roots)} configured Confluence page trees could not be read. "
                "Check that the pages still exist and that the account can read them."
            )
            logger.error(f"Confluence integration {integration_id}: {message}")
            update_integration_kb_load_result(integration_id, "error", error_message=message)
            return []

        # Spaces were discovered but not one page could be read. On-premise
        # instances restrict spaces per-account, so this is the shape a
        # permissions problem takes — reporting it as a healthy empty load hides
        # the one fact the operator needs.
        if space_keys and not documents and stats["empty_spaces"] == len(space_keys):
            message = (
                f"None of the {len(space_keys)} visible Confluence spaces returned any pages. "
                "The credentials can list spaces but cannot read their content — check the "
                "account's space permissions."
            )
            logger.error(f"Confluence integration {integration_id}: {message}")
            update_integration_kb_load_result(integration_id, "error", error_message=message)
            return []

        document_ids: List[str] = []
        if documents:
            logging.info(f"Processing {len(documents)} Confluence documents into collection {collection_name}...")
            _, document_ids = process_documents(
                documents,
                embeddings,
                module="knowledge_base",
                collection_name=collection_name,
                source="confluence",
                tenant_id=tenant_id,
                trigger_type=trigger_type,
                triggered_by=triggered_by,
            )
            if not document_ids:
                # process_documents swallows embedding failures and returns no
                # ids — a non-empty scrape that embedded nothing is a failed
                # load, not a healthy 'active' one.
                logger.error(f"Embedding produced no documents for Confluence integration {integration_id}")
                update_integration_kb_load_result(
                    integration_id, "error", error_message="Embedding produced no documents from the Confluence scrape"
                )
                return []
        update_integration_kb_load_result(integration_id, "active", len(document_ids))
        return document_ids
    except Exception as e:
        logger.error(f"Failed to process Confluence integration {integration_id}: {e}")
        update_integration_kb_load_result(integration_id, "error", error_message=str(e))
        return []


def load_confluence_docs(
    account_ids: List[str] | None,
    force: bool = False,
    integration_ids: List[str] | None = None,
    trigger_type: str = "system_sync",
    triggered_by: str = "system",
):
    """
    Load Confluence docs at the tenant level (integrations are tenant-scoped).

    Each integration is scraped exactly once per run regardless of how many
    cloud accounts belong to the tenant — the resulting collection
    ``{integration_id}_knowledge_base`` is tagged with ``tenant_id`` so every
    account in that tenant sees it via search-time metadata filtering.

    Args:
        account_ids: Optional filter. When provided, only tenants that include
            at least one of these accounts are processed.
        force: currently unused, kept for API parity.
        integration_ids: Optional filter. When provided, only these integrations
            are scraped (used by the per-integration retrigger).
        trigger_type: load-history trigger type (``system_sync`` or ``user_retrigger``).
        triggered_by: user id, or ``system`` for periodic syncs.
    """
    logging.info("Processing Confluence documents (tenant-scoped)...")
    logger.info(f"Force param is not used in this function. Force:{force}")

    active_tenants = list_active_tenants()
    logger.info(f"Found {len(active_tenants)} active tenants for Confluence scrape")

    for tenant_id in active_tenants:
        tenant_accounts = get_active_accounts_for_tenant(tenant_id)
        if not tenant_accounts:
            logger.warning(f"Skipping tenant {tenant_id}: no active accounts")
            continue
        # Honour account_ids at tenant granularity — scrape the tenant if ANY
        # of its accounts is in the requested set. Previously we only checked
        # the arbitrary first account, which could skip tenants unfairly.
        if account_ids and not any(str(a) in account_ids for a in tenant_accounts):
            logger.info(f"Skipping tenant {tenant_id}: no matching account in filter {account_ids}")
            continue
        integrations = get_confluence_integrations_for_tenant(tenant_id)
        if not integrations:
            logging.debug(f"No Confluence integrations for tenant {tenant_id}")
            continue

        logger.info(f"Processing {len(integrations)} Confluence integrations for tenant {tenant_id}")
        embedding_account_id = tenant_accounts[0]
        embeddings = get_embeddings(embedding_account_id)

        for integration in integrations:
            integration_id = integration["integration_id"]
            # integration_id is a UUID object from the DB; integration_ids holds
            # plain strings from the request — compare as strings.
            if integration_ids and str(integration_id) not in integration_ids:
                continue
            # Per-integration lock so a periodic sync and a retrigger can't
            # scrape the same integration at once.
            lock = FileLock(f"{lock_dir}/integration_{integration_id}.lock", timeout=0)
            try:
                lock.acquire(blocking=False)
            except Timeout:
                logger.info(f"Integration {integration_id} is already syncing, skipping")
                continue
            try:
                integration_document_ids = _process_integration(
                    integration, tenant_id, embeddings, trigger_type, triggered_by
                )
                if integration_document_ids:
                    # Prune stale docs within this integration's collection only.
                    collection_name = f"{integration_id}_knowledge_base"
                    logger.info(
                        f"Handle updated documents for tenant_id: {tenant_id}, "
                        f"integration_id: {integration_id}, collection: {collection_name}"
                    )
                    handle_updated_documents(collection_name, integration_document_ids)
            finally:
                lock.release()


# ServiceNow Knowledge Base Integration Functions


def extract_kb_content(html_content):
    """Extract text from ServiceNow KB article HTML using BeautifulSoup."""
    if not html_content:
        return ""
    soup = BeautifulSoup(html_content, "html.parser")
    for tag in soup(["script", "style", "nav", "header", "footer", "aside"]):
        tag.decompose()
    return soup.get_text(separator="\n", strip=True)


def get_kb_article_url(base_url, kb_number):
    """Construct ServiceNow KB article URL."""
    base_url = base_url.rstrip("/")
    if not base_url.startswith("http"):
        base_url = f"https://{base_url}"
    return f"{base_url}/kb_view.do?sysparm_article={kb_number}"


# Inherited parent columns we never want from the extension table — these are
# either already on the article row or are editor/instrumentation metadata
# (``kb_category``, ``kb_knowledge_base``) we shouldn't embed.
_KB_EXTENSION_SKIP_COLUMNS = frozenset({"kb_category", "kb_knowledge_base"})


def fetch_servicenow_kb_extension_content(snow_client, sys_class_name, sys_id, kb_number=""):
    """Fetch template-specific body fields from a KB article's extension table.

    OOB ServiceNow KB templates (``kb_template_known_error_article``,
    ``kb_template_how_to_article``, ``kb_template_faq_article``, ...) all prefix
    their body columns with ``kb_`` — e.g. ``kb_cause``, ``kb_workaround``,
    ``kb_description``, ``kb_question``, ``kb_answer``. Other columns on the
    extension row are editor/instrumentation noise (``editor_type``,
    ``article_id``, ``instrumentation_metadata``, ``generated_with_now_assist``,
    ...), so we whitelist the ``kb_`` prefix. Template-agnostic: new OOB
    templates need no code changes.
    """
    if not sys_class_name or sys_class_name == "kb_knowledge" or not sys_id:
        return ""
    # `sys_class_name` is interpolated into the API path, so reject anything
    # that doesn't match a ServiceNow table-name shape (defense-in-depth
    # against a misconfigured / hostile upstream returning unexpected values).
    if not re.fullmatch(r"[a-z][a-z0-9_]*", sys_class_name):
        logger.warning(
            f"Refusing to fetch extension content for {kb_number}: " f"unexpected sys_class_name={sys_class_name!r}"
        )
        return ""
    try:
        resource = snow_client.resource(api_path=f"/table/{sys_class_name}")
        record = resource.get(query={"sys_id": sys_id}).one_or_none()
        if not record:
            return ""
        parts = []
        for field_name, value in record.items():
            if not field_name.startswith("kb_") or field_name in _KB_EXTENSION_SKIP_COLUMNS:
                continue
            # Reference fields come back as dicts ({"link": ..., "value": ...});
            # only long-text/string content columns matter for the body.
            if not isinstance(value, str) or not value.strip():
                continue
            # Use a plain-text section header (not <h3>) so an unexpected
            # field_name can't synthesize HTML that bypasses the script/style
            # stripper in extract_kb_content.
            parts.append(f"## {field_name}\n\n{value}")
        return "\n\n".join(parts)
    except Exception as e:
        logger.warning(
            f"Extension-table fallback failed for {kb_number} "
            f"(sys_class_name={sys_class_name}, sys_id={sys_id}): {e}"
        )
        return ""


def fetch_servicenow_kb_articles(snow_client, batch_size=50):
    """Fetch all published KB articles from ServiceNow with pagination."""
    try:
        kb_resource = snow_client.resource(api_path="/table/kb_knowledge")

        all_articles = []
        offset = 0

        while True:
            logger.info(f"Fetching ServiceNow KB articles: offset={offset}, limit={batch_size}")

            # Set query parameters for published articles
            response = kb_resource.get(query={"workflow_state": "published"}, limit=batch_size, offset=offset)

            batch = response.all()
            if not batch:
                break
            all_articles.extend(batch)
            offset += batch_size
            logger.info(f"Fetched {len(batch)} articles, total: {len(all_articles)}")

        logger.info(f"Total KB articles fetched: {len(all_articles)}")
        return all_articles

    except Exception as e:
        logger.error(f"Failed to fetch ServiceNow KB articles: {e}")
        return []


def process_servicenow_kb_article(article, base_url, snow_client=None):
    """Process a single ServiceNow KB article into a Document."""
    try:
        sys_id = article.get("sys_id", "")
        kb_number = article.get("number", "")
        short_description = article.get("short_description", "")
        keywords = article.get("keywords", "")
        article_type = article.get("article_type", "")
        sys_class_name = article.get("sys_class_name", "")
        sys_updated_on = article.get("sys_updated_on", "")

        # Pick whichever of text/wiki has more body — ServiceNow KB articles
        # can have a stale `text` from the legacy editor AND a populated `wiki`
        # from the current editor (or vice versa). Old behavior of "first
        # truthy wins" silently lost content.
        text_body = article.get("text") or ""
        wiki_body = article.get("wiki") or ""
        if len(text_body) >= len(wiki_body):
            body_field, html_text = "text", text_body
        else:
            body_field, html_text = "wiki", wiki_body

        text_content = extract_kb_content(html_text)

        # Extended templates (e.g. Known Error) store body on extension tables
        # that /table/kb_knowledge doesn't project. Fall back when the
        # *extracted* content is empty, not just when html_text is empty — SNOW
        # often returns boilerplate like `<p><br></p>` that strips to nothing.
        if not text_content and snow_client is not None:
            html_text = fetch_servicenow_kb_extension_content(snow_client, sys_class_name, sys_id, kb_number)
            if html_text:
                body_field = sys_class_name
                text_content = extract_kb_content(html_text)
        if not text_content:
            logger.warning(
                f"No content extracted from KB article {kb_number} "
                f"(article_type={article_type}, sys_class_name={sys_class_name}, "
                f"text_len={len(article.get('text') or '')}, "
                f"wiki_len={len(article.get('wiki') or '')})"
            )
            return None
        logger.debug(f"KB article {kb_number}: extracted {len(text_content)} chars from '{body_field}' field")

        article_url = get_kb_article_url(base_url, kb_number)

        # Build comprehensive page content
        page_content = f"Title: {short_description}\n\n{text_content}"
        if keywords:
            page_content += f"\n\nKeywords: {keywords}"
        if article_type:
            page_content += f"\nArticle Type: {article_type}"

        metadata = {
            "sys_id": sys_id,
            "kb_number": kb_number,
            "url": article_url,
            "article_type": article_type,
            "keywords": keywords,
            "last_updated": sys_updated_on,
        }

        return Document(page_content=page_content, metadata=metadata)

    except Exception as e:
        logger.error(f"Failed to process KB article {article.get('number', 'unknown')}: {e}")
        return None


def collect_servicenow_kb_documents(snow_client, base_url):
    """Fetch all published ServiceNow KB articles and return them as documents.

    Embedding is done in a single process_documents call per integration so
    the load history records one row per sync, not one per article batch.
    """
    articles = fetch_servicenow_kb_articles(snow_client)
    documents: List[Document] = []
    for article in articles:
        doc = process_servicenow_kb_article(article, base_url, snow_client)
        if doc:
            documents.append(doc)
    logger.info(f"Collected {len(documents)} ServiceNow KB documents from {len(articles)} articles")
    return documents


def _process_servicenow_integration(
    integration, tenant_id, embeddings, trigger_type="system_sync", triggered_by="system"
):
    """Scrape one ServiceNow integration and embed it as a single load.

    rag-server owns the integration KB's status flip — set to 'active' on
    completion (even with zero articles) or 'error' on failure.
    """
    config = integration["config"]
    integration_id = integration["integration_id"]
    instance_url = config.get("url", "")
    username = config.get("username", "")
    password = config.get("password", "")
    collection_name = f"{integration_id}_knowledge_base"

    if not all([instance_url, username, password]):
        logger.error(f"Missing ServiceNow config for integration {integration_id}")
        update_integration_kb_load_result(
            integration_id, "error", error_message="Missing ServiceNow connection config (url, username, or password)"
        )
        return []

    try:
        # pysnow expects the instance name, not the full URL.
        # URL format: https://dev183745.service-now.com or dev183745.service-now.com
        instance_name = instance_url.replace("https://", "").replace("http://", "").split(".")[0]
        logger.info(f"Connecting to ServiceNow instance: {instance_name}")
        client = pysnow.Client(instance=instance_name, user=username, password=password)

        documents = collect_servicenow_kb_documents(client, instance_url)
        document_ids: List[str] = []
        if documents:
            logger.info(f"Processing {len(documents)} ServiceNow KB documents into collection {collection_name}...")
            _, document_ids = process_documents(
                documents,
                embeddings,
                module="knowledge_base",
                collection_name=collection_name,
                source="servicenow",
                tenant_id=tenant_id,
                trigger_type=trigger_type,
                triggered_by=triggered_by,
            )
            if not document_ids:
                # process_documents swallows embedding failures and returns no
                # ids — a non-empty scrape that embedded nothing is a failed
                # load, not a healthy 'active' one.
                logger.error(f"Embedding produced no documents for ServiceNow integration {integration_id}")
                update_integration_kb_load_result(
                    integration_id, "error", error_message="Embedding produced no documents from the ServiceNow scrape"
                )
                return []
        update_integration_kb_load_result(integration_id, "active", len(document_ids))
        return document_ids

    except Exception as e:
        logger.error(f"Failed to process ServiceNow KB integration {integration_id}: {e}")
        update_integration_kb_load_result(integration_id, "error", error_message=str(e))
        return []


def load_servicenow_kb(
    account_ids: List[str] | None,
    force: bool = False,
    integration_ids: List[str] | None = None,
    trigger_type: str = "system_sync",
    triggered_by: str = "system",
):
    """
    Load ServiceNow KB articles at the tenant level (integrations are tenant-scoped).

    Each integration is scraped exactly once per run regardless of how many
    cloud accounts belong to the tenant — the resulting collection
    ``{integration_id}_knowledge_base`` is tagged with ``tenant_id`` so every
    account in that tenant sees it via search-time metadata filtering.

    Args:
        account_ids: Optional filter. When provided, only tenants that include
            at least one of these accounts are processed.
        force: currently unused, kept for API parity.
        integration_ids: Optional filter. When provided, only these integrations
            are scraped (used by the per-integration retrigger).
        trigger_type: load-history trigger type (``system_sync`` or ``user_retrigger``).
        triggered_by: user id, or ``system`` for periodic syncs.
    """
    logging.info("Processing ServiceNow KB documents (tenant-scoped)...")
    logger.info(f"Force param not used. Force: {force}")

    active_tenants = list_active_tenants()
    logger.info(f"Found {len(active_tenants)} active tenants for ServiceNow scrape")

    for tenant_id in active_tenants:
        tenant_accounts = get_active_accounts_for_tenant(tenant_id)
        if not tenant_accounts:
            logger.warning(f"Skipping tenant {tenant_id}: no active accounts")
            continue
        # Match tenant if ANY of its accounts is in the requested account_ids set.
        if account_ids and not any(str(a) in account_ids for a in tenant_accounts):
            logger.info(f"Skipping tenant {tenant_id}: no matching account in filter {account_ids}")
            continue

        integrations = get_servicenow_kb_integrations_for_tenant(tenant_id)
        if not integrations:
            logging.debug(f"No ServiceNow KB integrations for tenant {tenant_id}")
            continue

        logger.info(f"Processing {len(integrations)} ServiceNow KB integrations for tenant {tenant_id}")
        embedding_account_id = tenant_accounts[0]
        embeddings = get_embeddings(embedding_account_id)

        for integration in integrations:
            integration_id = integration["integration_id"]
            # integration_id is a UUID object from the DB; integration_ids holds
            # plain strings from the request — compare as strings.
            if integration_ids and str(integration_id) not in integration_ids:
                continue
            # Per-integration lock so a periodic sync and a retrigger can't
            # scrape the same integration at once.
            lock = FileLock(f"{lock_dir}/integration_{integration_id}.lock", timeout=0)
            try:
                lock.acquire(blocking=False)
            except Timeout:
                logger.info(f"Integration {integration_id} is already syncing, skipping")
                continue
            try:
                integration_document_ids = _process_servicenow_integration(
                    integration, tenant_id, embeddings, trigger_type, triggered_by
                )
                if integration_document_ids:
                    collection_name = f"{integration_id}_knowledge_base"
                    logger.info(
                        f"Handling updated documents for tenant {tenant_id}, "
                        f"integration_id: {integration_id}, collection: {collection_name}"
                    )
                    handle_updated_documents(collection_name, integration_document_ids)
            finally:
                lock.release()


# Nudgebee Product Documentation Functions


def _fetch_nudgebee_sitemap_urls() -> List[str]:
    """Fetch and parse sitemap.xml to get all documentation page URLs."""
    base_url = Config.nudgebee_docs_url
    sitemap_url = f"{base_url.rstrip('/')}/sitemap.xml"
    logger.info(f"Fetching Nudgebee docs sitemap from: {sitemap_url}")

    try:
        response = requests.get(sitemap_url, timeout=30)
        response.raise_for_status()
    except Exception as e:
        logger.error(f"Failed to fetch sitemap from {sitemap_url}: {e}")
        return []

    try:
        root = ET.fromstring(response.content)
        # Sitemap XML uses namespace
        namespace = {"ns": "http://www.sitemaps.org/schemas/sitemap/0.9"}
        # The sitemap may contain URLs with a different host (e.g. app.nudgebee.com)
        # than the actual docs site. Rewrite them to use the docs base URL.
        base_parsed = urlparse(base_url)
        base_scheme_host = f"{base_parsed.scheme}://{base_parsed.netloc}"

        urls = []
        skipped = 0
        for url_elem in root.findall(".//ns:url/ns:loc", namespace):
            page_url = url_elem.text
            if page_url:
                page_url = page_url.strip()
                # Only include /docs/ pages (filter out non-doc pages like /markdown-page/)
                if "/docs/" not in page_url:
                    skipped += 1
                    continue
                # Filter out release archive pages to avoid noise
                if "/release/archive/" in page_url:
                    skipped += 1
                    continue
                # Rewrite URL to use the docs host
                parsed = urlparse(page_url)
                page_url = f"{base_scheme_host}{parsed.path}"
                urls.append(page_url)

        if skipped:
            logger.info(f"Skipped {skipped} non-documentation URLs from sitemap")
        logger.info(f"Found {len(urls)} page URLs in sitemap")
        return urls
    except ET.ParseError as e:
        logger.error(f"Failed to parse sitemap XML: {e}")
        return []


def _extract_nudgebee_doc_content(html_content: str) -> tuple:
    """Extract main content and title from a Nudgebee docs page."""
    soup = BeautifulSoup(html_content, "html.parser")

    # Extract title
    title = ""
    title_tag = soup.find("title")
    if title_tag:
        title = title_tag.get_text(strip=True)

    # Extract main article content (Docusaurus uses <article> or <main>)
    article = soup.find("article") or soup.find("main")
    if not article:
        # Fallback to body content
        article = soup.find("body")

    if not article or isinstance(article, NavigableString):
        return "", title

    return html_to_markdown(article, drop_page_furniture=True), title


def _get_section_from_url(page_url: str, base_url: str) -> str:
    """Extract section path from URL for metadata."""
    parsed = urlparse(page_url)
    path = parsed.path.strip("/")
    # Remove 'docs/' prefix if present
    if path.startswith("docs/"):
        path = path[5:]
    return path


def _fetch_nudgebee_doc_page(page_url: str, base_url: str) -> Document | None:
    """Fetch a single Nudgebee doc page and return a Document or None."""
    try:
        resp = requests.get(page_url, timeout=30)
        resp.raise_for_status()
    except Exception as e:
        logger.warning(f"Failed to fetch Nudgebee doc page {page_url}: {e}")
        return None

    content, title = _extract_nudgebee_doc_content(resp.text)
    if not content:
        logger.info(f"No content extracted from {page_url}, skipping")
        return None

    section = _get_section_from_url(page_url, base_url)
    page_content = f"Title: {title}\n\n{content}" if title else content

    return Document(
        page_content=page_content,
        metadata={
            "url": page_url,
            "title": title,
            "section": section,
        },
    )


def _fetch_nudgebee_doc_batch(urls: List[str], base_url: str, start_idx: int, total_urls: int) -> List[Document]:
    """Fetch a batch of Nudgebee doc pages and return successfully fetched documents."""
    docs = []
    for idx, page_url in enumerate(urls, start_idx + 1):
        logger.info(f"Fetching page {idx}/{total_urls}: {page_url}")
        doc = _fetch_nudgebee_doc_page(page_url, base_url)
        if doc:
            docs.append(doc)
    return docs


def load_nudgebee_docs(account_ids: List[str] | None = None, force: bool = False):
    """
    Load Nudgebee product documentation from the Docusaurus docs site.
    Fetches and processes pages in batches to limit memory usage.

    Args:
        account_ids: Not used (product docs are global).
        force: Not used currently.
    """
    logger.info("Processing Nudgebee product documentation...")
    logger.info(f"Force param: {force}, account_ids param: {account_ids} (not used, docs are global)")

    urls = _fetch_nudgebee_sitemap_urls()
    if not urls:
        logger.warning("No URLs found in Nudgebee docs sitemap. Skipping.")
        return

    total_urls = len(urls)
    fetch_batch_size = Config.nudgebee_docs_fetch_batch_size
    base_url = Config.nudgebee_docs_url
    embeddings = get_embeddings("")
    all_document_ids = []
    collection_name = get_collection_name(None, "nudgebee_docs")
    assert collection_name is not None
    total_batches = (total_urls + fetch_batch_size - 1) // fetch_batch_size

    # Create vector store ONCE before the loop to avoid per-batch collection checks
    from rag.vector_store import ensure_collection_exists

    # Tag with the unified ``knowledge_base`` module so this global collection
    # is searchable alongside user KBs and integration-sourced content via a
    # single tool in llm-server. Collection name stays ``nudgebee_docs`` and
    # ``account`` stays ``global`` — filter logic matches both global and
    # per-account collections when module matches. ``source`` lets callers
    # filter to just product docs via ``metadata_filter={"source": "nudgebee_docs"}``.
    collection_metadata = {
        "module": "knowledge_base",
        "account": "global",
        "source": "nudgebee_docs",
        "embeddings_generated_at": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime()),
    }
    vector_store = ensure_collection_exists(
        embedding_function=embeddings,
        collection_name=collection_name,
        collection_metadata=collection_metadata,
    )

    logger.info(f"Fetching and processing {total_urls} Nudgebee doc pages in batches of {fetch_batch_size}...")

    for batch_num, batch_start in enumerate(range(0, total_urls, fetch_batch_size), 1):
        batch_urls = urls[batch_start : batch_start + fetch_batch_size]
        logger.info(f"Fetching page batch {batch_num}/{total_batches} ({len(batch_urls)} pages)...")

        batch_docs = _fetch_nudgebee_doc_batch(batch_urls, base_url, batch_start, total_urls)
        if not batch_docs:
            continue

        logger.info(f"Processing batch {batch_num}/{total_batches}: {len(batch_docs)} documents...")
        _, doc_ids = process_documents(
            batch_docs,
            embeddings,
            account_id=None,
            module="knowledge_base",
            collection_name=collection_name,
            vector_store=vector_store,
            source="nudgebee_docs",
        )

        if doc_ids:
            all_document_ids.extend(doc_ids)

    if all_document_ids and collection_name:
        handle_updated_documents(collection_name, all_document_ids)

    logger.info(f"Finished processing Nudgebee docs. Total documents: {len(all_document_ids)}")
