import asyncio
import gc
import json
import logging
import re
import time
from concurrent.futures import ThreadPoolExecutor
from typing import List, Dict, Any, TYPE_CHECKING

from rag.qdrant.client import get_qdrant_client
from rag.qdrant.client import list_collections_optimized

if TYPE_CHECKING:
    pass

from rag.core.embeddings.generator import get_llm
from utils.config import Config
from utils.shared import get_http_session_with_retry

# Set up logging
logger = logging.getLogger(__name__)

headers = {"Content-Type": "application/json", "X-SECRET-KEY": Config.relay_server_secret}


def fetch_metrics_from_server(account_id: str) -> List[str]:
    """Fetch available metrics from the Prometheus server"""
    try:
        response = _make_metrics_request(account_id)
        if not response or response.status_code != 200:
            _log_request_error(response)
            return []

        data = response.json()
        return _extract_and_process_metrics(data)
    except Exception as e:
        logger.warning(f"Failed to fetch metrics from relay server for account: {account_id}, Reason: {e}")
        return []


def _make_metrics_request(account_id: str):
    """Make HTTP request to fetch metrics from the Prometheus server"""
    logger.info(f"Fetching metrics from server: {Config.relay_server_url}")
    payload = json.dumps(
        {
            "no_sinks": True,
            "body": {
                "account_id": account_id,
                "action_name": "prometheus_labels",
                "action_params": {"label_name": "__name__"},
            },
            "cache": False,
        }
    )

    return get_http_session_with_retry().request(
        "POST", Config.relay_server_url, headers=headers, data=payload, timeout=10
    )


def _log_request_error(response):
    """Log detailed information about a failed request"""
    status = response.status_code if response else "No response"
    response_text = response.text if response else "N/A"
    logger.warning(f"Failed to fetch metrics list. Status: {status}, Response: {response_text}")


def _extract_and_process_metrics(data) -> List[str]:
    """Extract and process metrics from the response data"""
    evidence_data = _get_evidence_data(data)
    if not evidence_data:
        return []

    metrics = _extract_metrics_from_evidence(evidence_data)
    return _process_metrics_list(metrics)


def _get_evidence_data(data):
    """Extract evidence data from the response data structure"""
    if "data" not in data:
        logger.warning(f"Failed to fetch metrics from server. Response: {data}")
        return None

    findings = data["data"].get("findings", [])
    if not findings:
        logger.warning("Failed to fetch metrics from server. No findings.")
        return None

    first_finding = findings[0]
    evidence_list = first_finding.get("evidence", [])
    if not evidence_list:
        logger.warning("Failed to fetch metrics from server. No evidence.")
        return None

    first_evidence = evidence_list[0]
    if "data" not in first_evidence:
        logger.warning("Failed to fetch metrics from server. No data in evidence.")
        return None

    return first_evidence["data"]


def _extract_metrics_from_evidence(evidence_data):
    """Extract metrics list from evidence data"""
    try:
        data = json.loads(evidence_data)
        if not data or len(data) == 0 or "data" not in data[0]:
            logger.warning("Invalid data format in evidence")
            return []

        data_list_str = data[0]["data"]
        data_dict = json.loads(data_list_str)
        return data_dict.get("data", [])
    except json.JSONDecodeError as e:
        logger.warning(f"Failed to decode JSON data: {e}")
        return []


def _process_metrics_list(metrics) -> List[str]:
    """Process the metrics list - filter and deduplicate"""
    if not metrics:
        return []

    logger.info(f"Fetched {len(metrics)} metrics from server")
    filtered_metrics = [metric for metric in metrics if metric]
    return list(set(filtered_metrics))


def extract_metric_names(documents: List[Dict[str, Any]]) -> set:
    """Extract unique metric names from the documents"""
    metric_names = set()

    for doc in documents:
        # Extract metric name from metadata.metric field
        if "metadata" in doc and "metric" in doc["metadata"]:
            metric_name = doc["metadata"]["metric"]
            metric_names.add(metric_name)

    logger.info(f"Extracted {len(metric_names)} unique metrics")
    return metric_names


def parse_json_string(json_string_with_code_block):
    """Parse JSON string, handling various Markdown code block formats"""
    try:
        json_string = _extract_json_from_markdown(json_string_with_code_block)
        return _try_parse_json(json_string)
    except Exception as e:
        logger.warning(f"Failed to parse JSON string: {json_string_with_code_block}, Reason: {e}")
        return None


def _extract_json_from_markdown(text):
    """Extract JSON content from various Markdown code block formats"""
    if text.startswith("```json") and text.endswith("```"):
        return text[7:-3].strip()
    elif text.startswith("```") and text.endswith("```"):
        return text[3:-3].strip()
    elif text.startswith("```") and text.endswith("```json"):
        return text[3:-7].strip()
    elif text.startswith("```"):
        return text[3:].strip()
    else:
        return text.strip()


def _normalize_metadata(data):
    """Convert 'name' field to 'metric' in metadata if present"""
    for item in data:
        if "metadata" in item and "name" in item["metadata"]:
            item["metadata"]["metric"] = item["metadata"].pop("name")
    return data


def _try_parse_json(json_string):
    """Attempt to parse JSON with fallback to regex pattern matching"""
    try:
        data = json.loads(json_string)
        return _normalize_metadata(data)
    except json.JSONDecodeError:
        return _handle_json_parse_error(json_string)


def _handle_json_parse_error(json_string):
    """Handle JSON parsing errors by attempting to extract valid JSON patterns"""
    json_data = []
    json_pattern = re.search(r"\[[\s\S]*?\{[\s\S]*?}[\s\S]*?]", json_string)

    if not json_pattern:
        logger.warning(f"Could not find valid JSON in the provided string, returning empty list, input: {json_string}")
        return json_data

    # Try to parse each matched group
    for group in json_pattern.groups() or []:
        if not group:
            logger.warning("Empty pattern group found")
            continue

        try:
            data = json.loads(group)
            json_data.append(data)
        except json.JSONDecodeError:
            logger.warning(f"Failed to parse JSON pattern: {group[:100]}...")

    return json_data


def generate_metric_example(metric_name: str, num_variations: int = 5) -> List[Dict[str, Any]]:
    """Generate example documents for a new metric"""
    llm = get_llm("")
    base_prompt = (
        f"You are an AI assistant that generates synthetic training data for Prometheus metrics.\n\n"
        f'Metric name: "{metric_name}"\n'
        f"Metadata (optional): description and labels may or may not be provided.\n\n"
        f"If metadata is not available, use only the metric name and infer its purpose based on naming patterns and common usage. Do not repeat the metric name verbatim in the questions.\n\n"  # noqa: E501 W291
        f"Your task is to:\n"
        f"1. Understand the metric (and metadata if provided).\n"
        f"2. Generate at least {num_variations} natural language questions a user might realistically ask. These questions should reflect real-world phrasing — avoid simply rewording or repeating the metric name. Instead, write what a user would actually ask in plain language, even if they don't know the exact metric name.\n"  # noqa: E501 W291
        f"3. Generate a PromQL query that answers each question.\n"
        f'4. Include metadata: name (always include), description (use provided or ""), labels (use provided or []).\n\n'  # noqa: E501 W291
        f"Output format:\n"
        f"A JSON array ONLY. No explanations or markdown. Each item must follow this format:\n"
        f"{{\n"
        f'  "question": "<natural language question>",\n'
        f'  "answer": "<valid PromQL query>",\n'
        f'  "metadata": {{\n'
        f'    "name": "{metric_name}",\n'
        f'    "description": "<description or empty string>",\n'
        f'    "labels": ["<label1>", "<label2>", ...] or []\n'
        f"  }}\n"
        f"}}\n\n"
        f"Example:\n"
        f"[\n"
        f"  {{\n"
        f'    "question": "How much time has the CPU spent idle over the past 5 minutes?",\n'
        f'    "answer": "avg_over_time({metric_name}[5m])",\n'
        f'    "metadata": {{\n'
        f'      "name": "{metric_name}",\n'
        f'      "description": "",\n'
        f'      "labels": []\n'
        f"    }}\n"
        f"  }},\n"
        f"  ...\n"
        f"]\n"
    )
    documents = []
    max_retries = Config.max_retry_count

    for attempt in range(1, max_retries + 1):
        try:
            logger.info(f"Generating examples for metric {metric_name} (attempt {attempt}/{max_retries})")
            result = llm.generate(base_prompt)
            response_text = re.sub(r"\s+", " ", result.text).strip()
            json_response = parse_json_string(response_text)

            if not json_response:
                logger.warning(
                    f"Attempt {attempt}/{max_retries}: Failed to parse JSON response for metric {metric_name}"
                )
                if attempt == max_retries:
                    logger.warning(f"All {max_retries} attempts failed. Last response: {response_text[:200]}...")
                    return []
                continue  # Try again

            # If we got a valid response, process it
            for doc in json_response:
                document = {
                    "page_content": json.dumps(doc),
                    "metadata": {
                        "metric": metric_name,
                        "source": "auto-generated",
                        "generated_at": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime()),
                    },
                }
                documents.append(document)

            logger.info(
                f"Successfully generated {len(documents)} examples for metric {metric_name} on attempt {attempt}"
            )
            break  # Success, exit the retry loop

        except Exception as e:
            logger.warning(f"Attempt {attempt}/{max_retries}: Error generating examples for metric {metric_name}: {e}")
            if attempt == max_retries:
                return []

    return documents


async def process_metrics_in_batches(metrics: List[str], batch_size: int = 5) -> List[Dict[str, Any]]:
    """Process metrics in parallel batches"""
    all_documents = []
    total_metrics = len(metrics)

    # Create a thread pool executor
    with ThreadPoolExecutor(max_workers=batch_size) as executor:
        loop = asyncio.get_running_loop()

        for i in range(0, total_metrics, batch_size):
            batch = metrics[i : i + batch_size]
            batch_size_actual = len(batch)
            logger.info(
                f"Processing batch {i // batch_size + 1}/{(total_metrics + batch_size - 1) // batch_size} "
                f"with {batch_size_actual} metrics"
            )

            # Create tasks to run in thread pool
            futures = [loop.run_in_executor(executor, generate_metric_example, metric, batch_size) for metric in batch]

            # Wait for all tasks to complete
            batch_results = await asyncio.gather(*futures)

            # Process results
            for j, results in enumerate(batch_results):
                metric_idx = i + j + 1
                metric_name = batch[j]
                logger.info(
                    f"Completed metric {metric_idx}/{total_metrics}: '{metric_name}' with {len(results)} examples"
                )
                all_documents.extend(results)

    return all_documents


def get_prometheus_metrics_examples(new_metrics: List[str]):
    """Generate examples for new metrics using parallel processing"""
    if not new_metrics:
        logger.info("No new metrics to add")
        return []

    logger.info(f"Generating examples for {len(new_metrics)} new metrics")

    # Use asyncio to run the metrics processing in parallel
    return asyncio.run(process_metrics_in_batches(new_metrics, batch_size=5))


def get_update_prometheus_metrics(account_id: str):
    """
    Fetch Prometheus metrics for an account and generate examples.

    IMPORTANT: This function REQUIRES scroll check to prevent duplicates!

    Why scroll is necessary:
    - Metric examples are LLM-generated (non-deterministic text)
    - Same metric generates different text each time
    - Different text = different MD5 hash = different document ID
    - Without scroll check: upsert creates DUPLICATES instead of updating

    Example:
        Run 1: LLM generates "CPU usage from 0-100" → doc_id "abc123"
        Run 2: LLM generates "CPU percentage 0 to 100" → doc_id "xyz789"
        Result: TWO documents for same metric (duplicate!)

    Performance trade-off:
    - WITH scroll: 30-60s overhead, but NO duplicates + fewer LLM calls
    - WITHOUT scroll: No overhead, but CREATES duplicates + wastes LLM API calls

    Args:
        account_id: Account ID to fetch metrics for

    Returns:
        List of metric examples (only for NEW metrics not in collection)
    """
    logger.info("Starting Prometheus metrics update process")

    # Fetch metrics from server
    server_metrics = fetch_metrics_from_server(account_id=account_id)

    if not server_metrics:
        logger.warning("No metrics received from server. Exiting.")
        return []

    logger.info(f"Server returned {len(server_metrics)} metrics for account {account_id}")

    # CRITICAL: Scroll to find existing metrics (prevents LLM-generated duplicates)
    logger.info("Checking existing metrics in collection (required for LLM-generated content)")
    existing_documents = get_metrics_from_embeddings(account_id, "prometheus")
    existing_metric_names = extract_metric_names(existing_documents)
    logger.info(f"Found {len(existing_metric_names)} existing metrics in collection")

    # Only generate examples for NEW metrics (not already in collection)
    new_metrics = list(set(server_metrics) - existing_metric_names)

    if not new_metrics:
        logger.info("No new metrics to process. All server metrics already exist in collection.")
        return []

    logger.info(f"Found {len(new_metrics)} new metrics to generate examples for")

    # Generate examples only for new metrics (saves LLM API calls + prevents duplicates)
    metrics_examples = get_prometheus_metrics_examples(new_metrics)
    del server_metrics
    del existing_documents
    del existing_metric_names
    del new_metrics
    gc.collect()

    if metrics_examples:
        logger.info(f"Generated {len(metrics_examples)} metric examples for new metrics")
        return metrics_examples
    else:
        logger.info("No metric examples generated")
        return []


def get_metrics_from_embeddings(account_id: str, module: str) -> List[Dict[str, Any]]:
    """Fetch metrics from embeddings database"""
    logger.info(
        f"Fetching metrics from embeddings for account_id: {account_id if account_id else 'global'} "
        f"and module: {module}"
    )
    try:
        collection_names = _find_relevant_collections(account_id, module)
        return _extract_metrics_from_collections(collection_names)
    except Exception as e:
        logger.warning(f"Error fetching metrics from embeddings: {e}")
        return []


def _find_relevant_collections(account_id: str, module: str) -> List[str]:
    """Find collections that match the account and module criteria"""
    matching_collections = []
    for collection in list_collections_optimized():
        metadata = collection.metadata
        if not metadata:
            continue

        if metadata.get("module") != module:
            continue

        collection_account = metadata.get("account")
        if collection_account == account_id or collection_account == "global":
            matching_collections.append(collection.name)

    return matching_collections


def _extract_metrics_from_collections(collection_names: List[str]) -> List[Dict[str, Any]]:
    """Process all matching collections and extract metrics"""
    if not collection_names:
        return []

    all_metrics = []
    for collection_name in collection_names:
        metrics = _process_single_collection(collection_name)
        all_metrics.extend(metrics)

    return all_metrics


def _process_single_collection(collection_name: str) -> List[Dict[str, Any]]:
    """Extract metrics from a single collection"""
    try:
        from rag.core.metadata_cache import get_metadata_cache
        from rag.qdrant.client import list_collections_optimized

        client = get_qdrant_client()

        # Check if collection exists in cache
        list_collections_optimized()
        if not get_metadata_cache().exists(collection_name):
            logger.warning(f"Collection {collection_name} does not exist")
            return []

        # Scroll through all documents to get their content
        documents = []
        offset = None

        while True:
            points, next_offset = client.scroll(
                collection_name=collection_name, limit=1000, offset=offset, with_payload=True, with_vectors=False
            )

            if not points:
                break

            for point in points:
                payload = point.payload or {}
                doc_content = payload.get("page_content", "")
                if doc_content:
                    documents.append(doc_content)

            if next_offset is None:
                break
            offset = next_offset

        return _parse_documents_to_metrics(documents)
    except Exception as e:
        logger.warning(f"Failed to parse documents from collection {collection_name}: {e}")
        return []


def _parse_documents_to_metrics(documents: List[str]) -> List[Dict[str, Any]]:
    """Convert document strings to metric dictionaries"""
    metrics = []
    for doc in documents:
        if not isinstance(doc, str) or not (doc.startswith("{") and doc.endswith("}")):
            logger.warning(f"Document is not a valid JSON string: {doc}")
            continue

        try:
            doc_dict = json.loads(doc)
            if doc_dict:
                metrics.append(doc_dict)
        except json.JSONDecodeError:
            logger.warning(f"Failed to parse document as JSON: {doc}")

    return metrics
