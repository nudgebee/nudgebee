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


def fetch_page_content(confluence, p_id):
    try:
        page = confluence.get_page_by_id(p_id, expand="body.storage")
        return page["body"]["storage"]["value"]
    except Exception as e:
        logger.warning(f"Failed to fetch page {p_id}: {e}")
        return None


def extract_content(content_html):
    soup = BeautifulSoup(content_html, "html.parser")
    # Only unpack two values: text and soup
    text = "\n".join([elem.get_text(strip=True) for elem in soup.find_all(["h1", "h2", "p", "li"])])
    return text, soup


def get_page_base_url(confluence):
    url = confluence.url
    # Normalize the base URL to ensure it correctly ends with /wiki
    url = url.rstrip("/")  # Remove any trailing slash first
    wiki = "/wiki"
    if wiki in url:
        # If /wiki is present, ensure the url ends with it,
        # stripping any further path components after the first '/wiki'.
        # Example: "http://host/path/wiki/extra" becomes "http://host/path/wiki"
        # Example: "http://host/path/wiki" remains "http://host/path/wiki"
        url = url.split(wiki, 1)[0] + wiki
    else:
        # If /wiki is not present at all, append it.
        # Example: "http://host/path" becomes "http://host/path/wiki"
        url += wiki
    return url


def process_page_batch(page_queue, visited_pages, confluence, space_key):
    batch = []

    for _ in range(min(Config.embedding_batch_size, len(page_queue))):
        page_id = page_queue.pop(0)
        if page_id in visited_pages:
            continue

        logger.info(f"Processing Confluence page ID: {page_id}")
        visited_pages.add(page_id)

        html_content = fetch_page_content(confluence, page_id)
        if not html_content:
            continue

        content, _ = extract_content(html_content)
        if content:
            url = get_page_base_url(confluence)
            page_url = f"{url}/spaces/{space_key}/pages/{page_id}"
            batch.append(Document(page_content=content, metadata={"page_id": page_id, "url": page_url}))

        # Extract linked pages
        child_pages = confluence.get_child_pages(page_id)
        for child in child_pages:
            child_id = child.get("id")
            if child_id and child_id not in visited_pages:
                page_queue.append(child_id)

    return batch


def collect_confluence_space_documents(confluence, space_key):
    """Walk a Confluence space and return all page documents (no embedding).

    Embedding is done in a single process_documents call per integration so
    the load history records one row per sync, not one per page batch.
    """
    visited_pages: set = set()
    all_pages = fetch_all_pages(confluence, space_key)
    page_queue = [page["id"] for page in all_pages]
    documents: List[Document] = []
    while page_queue:
        documents.extend(process_page_batch(page_queue, visited_pages, confluence, space_key))
    logging.info(f"Collected {len(documents)} Confluence page documents for space {space_key}")
    return documents


def fetch_all_spaces(confluence):
    try:
        spaces = confluence.get_all_spaces(start=0, limit=100)
        if isinstance(spaces, dict) and "results" in spaces and isinstance(spaces["results"], list):
            return [space["key"] for space in spaces["results"]]
        else:
            logger.warning("Unexpected format for spaces")
            return []
    except Exception as e:
        logger.warning(f"Failed to fetch spaces: {e}")
        return []


def _process_integration(integration, tenant_id, embeddings, trigger_type="system_sync", triggered_by="system"):
    """Scrape one Confluence integration and embed it as a single load.

    rag-server owns the integration KB's status flip — set to 'active' on
    completion (even with zero pages) or 'error' on failure.
    """
    config = integration["config"]
    integration_id = integration["integration_id"]
    collection_name = f"{integration_id}_knowledge_base"
    try:
        confluence = Confluence(
            url=config.get("host"),
            username=config.get("username"),
            password=config.get("token"),
        )
        space_key = config.get("namespace")
        space_keys = [space_key] if space_key else fetch_all_spaces(confluence)

        documents: List[Document] = []
        for sk in space_keys:
            documents.extend(collect_confluence_space_documents(confluence, sk))

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

    text = "\n".join(
        [elem.get_text(strip=True) for elem in article.find_all(["h1", "h2", "h3", "p", "li", "code", "pre"])]
    )
    return text, title


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
