"""
Configuration classes for RAG server.

Contains all configuration classes loaded from environment variables.
"""

import os
import tempfile
from enum import Enum

from dotenv import load_dotenv

load_dotenv()


class DBConfig:
    """Database configuration."""

    url = os.environ.get("APP_DATABASE_URL", "")
    if not url:
        raise ValueError("APP_DATABASE_URL environment variable not set")


class FileConfig:
    """File storage configuration."""

    base_path = os.environ.get("DATA_FILE_BASE_PATH", tempfile.gettempdir())
    qdrant_persistent_path = os.environ.get(
        "QDRANT_PATH", os.environ.get("CHROMA_PERSISTENT_PATH", "/nudgebee/rag-server/db")
    )


class Config:
    """Main configuration class for RAG server."""

    # General
    os.environ["TOKENIZERS_PARALLELISM"] = "true"
    max_token_length = int(os.environ.get("MAX_TOKEN_LENGTH", 8192))
    embedding_batch_size = int(os.environ.get("EMBEDDING_BATCH_SIZE", 10))
    max_concurrency = int(os.environ.get("MAX_CONCURRENCY", 2))
    gc_interval = int(os.environ.get("GC_INTERVAL", 10))  # Run gc.collect() every N batches per worker
    ondevice_num_threads = int(os.environ.get("ONDEVICE_NUM_THREADS", 2))  # torch inference threads
    ondevice_interop_threads = int(os.environ.get("ONDEVICE_INTEROP_THREADS", 1))  # torch inter-op threads
    ondevice_batch_size = int(os.environ.get("ONDEVICE_BATCH_SIZE", 4))  # batch size for on-device encode
    ondevice_backend = os.environ.get("ONDEVICE_BACKEND", "torch")  # "torch" (default, better accuracy) or "onnx"
    token_count_api_threshold = int(
        os.environ.get("TOKEN_COUNT_API_THRESHOLD", 100)
    )  # Use accurate API for token counting below this doc count, local approximation above
    avg_chars_per_token = int(
        os.environ.get("AVG_CHARS_PER_TOKEN", 4)
    )  # Approximate chars per token for local token estimation
    max_retry_count = int(os.environ.get("MAX_RETRY_COUNT", 5))
    max_retry_duration = int(os.environ.get("MAX_RETRY_DURATION", 600))
    initial_wait_time = int(os.environ.get("INITIAL_WAIT_TIME", 1))
    ranking_threshold = float(os.environ.get("RANKING_THRESHOLD", 0.5))
    rag_llm_provider_cache_ttl = int(
        os.environ.get("RAG_LLM_PROVIDER_CACHE_TTL", 1800)
    )  # LLM/embeddings provider config & client cache (default 30 min)
    rag_collection_cache_ttl = int(
        os.environ.get("RAG_COLLECTION_CACHE_TTL", 1800)
    )  # Qdrant collection list cache (default 30 min)
    reranking_max_docs = int(os.environ.get("RAG_RERANKING_MAX_DOCS", 10))  # max docs sent to LLM for reranking
    search_max_workers = int(
        os.environ.get("RAG_SEARCH_MAX_WORKERS", 8)
    )  # thread pool size for parallel collection search
    reranking_enabled = os.environ.get("RAG_RERANKING_ENABLED", "false").lower() == "true"  # LLM reranking default

    # Cross-encoder reranker. Runs in-process, so it needs no per-account LLM
    # credential — the LLM reranker it replaces failed 100% of calls on a stale
    # key and silently returned documents unranked.
    #
    # Benchmarked on 39 real queries taken from conversation history (29 needing
    # live data, 10 answerable from the KB). v2-m3 was the only model that
    # separated them cleanly — irrelevant results topped out at 0.775 while every
    # relevant one scored 0.900+, so a threshold anywhere in that gap is exact.
    # ms-marco-MiniLM is 16x cheaper but overlaps, and bge-reranker-base sits
    # between the two; both drop a genuine documentation answer at any threshold
    # that keeps junk out.
    #
    #   model                  errors/39   latency (8 docs, 2 threads)
    #   bge-reranker-v2-m3     0           1480ms  @ max_length 256
    #   bge-reranker-base      1            362ms
    #   ms-marco-MiniLM-L-6    1            309ms
    #
    # The model-cost column that used to sit here was measured the same wrong
    # way as the note below and has been removed rather than left misleading.
    #
    # The model costs ~1.1GB resident, not the 490MB an earlier revision of this
    # comment claimed: that figure was an RSS delta sampled immediately after
    # construction, and mmap'd weights fault in lazily, so most of them had not
    # been touched yet. Measured 1105MB locally and 1098MB in-pod after a full
    # warm-up pass. Nearly half of it is a 250k-token multilingual vocabulary we
    # never use on English documentation. To run somewhere smaller, set
    # RAG_RERANKER_MODEL=cross-encoder/ms-marco-MiniLM-L-6-v2 with
    # RAG_RERANKER_THRESHOLD=0.99 at the cost of missing roughly one
    # documentation question in ten.
    reranker_model = os.environ.get("RAG_RERANKER_MODEL", "BAAI/bge-reranker-v2-m3")
    # Minimum 0-1 relevance for a document to survive. 0.80 and 0.85 both scored
    # zero errors; 0.85 sits mid-gap rather than on its edge. Retune when the
    # model changes — this number is a property of the model, not of the corpus.
    reranker_threshold = float(os.environ.get("RAG_RERANKER_THRESHOLD", 0.85))
    # 256 tokens costs nothing in accuracy and cuts latency 3529ms -> 1480ms.
    # 192 is where separation collapses, so this is close to the floor.
    reranker_max_length = int(os.environ.get("RAG_RERANKER_MAX_LENGTH", 256))  # tokens per (query, doc) pair
    reranker_max_doc_chars = int(os.environ.get("RAG_RERANKER_MAX_DOC_CHARS", 1000))  # doc chars scored
    # int8 quantization: measured slower and larger on ARM (qnnpack), so it is
    # off until measured to help on the target platform (x86/fbgemm).
    reranker_quantize = os.environ.get("RAG_RERANKER_QUANTIZE", "false").lower() == "true"
    # Concurrent forward passes. ``get_matching_doc`` is a sync ``def``, so
    # FastAPI runs it on a 40-worker threadpool sized for I/O-bound work — which
    # this is not. Forty concurrent passes over a 2-core limit bought no
    # throughput (the work is CPU-bound) while each held its own activations:
    # the pod OOMKilled at 4Gi and p50 rerank latency reached 21.7s. Queueing is
    # cheaper than thrashing, so admit one pass at a time by default.
    # Clamped: a negative value makes threading.Semaphore raise at import and the
    # server never starts, and 0 blocks every rerank forever.
    reranker_max_concurrency = max(1, int(os.environ.get("RAG_RERANKER_MAX_CONCURRENCY", 1)))
    # Pairs per forward pass. Caps peak activation memory independently of how
    # many documents retrieval hands over. Clamped for the same reason: 0 raises
    # inside the batching loop and a negative value scores nothing at all.
    reranker_batch_size = max(1, int(os.environ.get("RAG_RERANKER_BATCH_SIZE", 4)))

    # Nudgebee Docs
    nudgebee_docs_url = os.environ.get("NUDGEBEE_DOCS_URL", "https://docs.nudgebee.com")
    nudgebee_docs_fetch_batch_size = int(os.environ.get("NUDGEBEE_DOCS_FETCH_BATCH_SIZE", 10))

    # Encryption
    nudgebee_encryption_key = os.environ.get("NUDGEBEE_ENCRYPTION_KEY", "")

    # Embeddings
    embeddings_provider = os.environ.get("EMBEDDINGS_PROVIDER", "bedrock")
    # Model Name for different embeddings providers
    # Bedrock - 'amazon.titan-embed-text-v2:0', Azure - 'text-embedding-ada-002',
    # Huggingface - 'text-embedding-ada-002', Ollama - 'nb-text-embeddings', OpenAI - 'text-embedding-ada-002'
    embeddings_model_id = os.environ.get("EMBEDDINGS_MODEL_NAME", "amazon.titan-embed-text-v2:0")
    embeddings_api_endpoint = os.environ.get("EMBEDDINGS_PROVIDER_API_ENDPOINT", "")
    embeddings_api_key = os.environ.get("EMBEDDINGS_PROVIDER_API_KEY", "")
    embeddings_api_version = os.environ.get("EMBEDDINGS_PROVIDER_API_VERSION", "")  # Azure - '2023-05-15'
    embeddings_api_type = os.environ.get("EMBEDDINGS_PROVIDER_API_TYPE", "")
    embeddings_region = os.environ.get("EMBEDDINGS_PROVIDER_REGION", "us-west-2")

    # Embedding output dimensions (Matryoshka truncation)
    _dimensions = os.environ.get("EMBEDDINGS_DIMENSIONS")
    embeddings_dimensions: int | None = int(_dimensions) if _dimensions else None

    # LLM
    # openai | aws_bedrock | sagemaker | azure | ollama | huggingface
    llm_provider = os.environ.get("LLM_PROVIDER_SUMMARY_AGENT") or os.environ.get("LLM_PROVIDER") or "bedrock"
    llm_model_name = (
        os.environ.get("LLM_MODEL_NAME_SUMMARY_AGENT")
        or os.environ.get("LLM_MODEL_NAME")
        or "meta.llama3-1-70b-instruct-v1:0"
    )
    llm_provider_api_endpoint = (
        os.environ.get("LLM_PROVIDER_API_ENDPOINT_SUMMARY_AGENT") or os.environ.get("LLM_PROVIDER_API_ENDPOINT") or ""
    )
    llm_provider_api_key = (
        os.environ.get("LLM_PROVIDER_API_KEY_SUMMARY_AGENT") or os.environ.get("LLM_PROVIDER_API_KEY") or ""
    )
    llm_provider_api_version = (
        os.environ.get("LLM_PROVIDER_API_VERSION_SUMMARY_AGENT") or os.environ.get("LLM_PROVIDER_API_VERSION") or ""
    )
    llm_provider_api_type = (
        os.environ.get("LLM_PROVIDER_API_TYPE_SUMMARY_AGENT") or os.environ.get("LLM_PROVIDER_API_TYPE") or ""
    )
    llm_provider_region = (
        os.environ.get("LLM_PROVIDER_REGION_SUMMARY_AGENT") or os.environ.get("LLM_PROVIDER_REGION") or "us-west-2"
    )
    # AWS Bedrock
    aws_region = os.environ.get("AWS_REGION", "us-west-2")
    aws_access_key = os.environ.get("aws_access_key_id", "")
    aws_secret_access_key = os.environ.get("aws_secret_access_key", "")

    # S3
    s3_bucket_name = os.environ.get("S3_BUCKET_NAME", "nudgebee-documents-v2")

    # Relay Server
    relay_server_url = os.environ.get("RELAY_SERVER_ENDPOINT", "http://localhost:8080")
    if not relay_server_url:
        raise ValueError("RELAY_SERVER_ENDPOINT environment variable not set")
    relay_server_url = relay_server_url + "request" if relay_server_url.endswith("/") else relay_server_url + "/request"
    relay_server_secret = os.environ.get("RELAY_SERVER_SECRET_KEY", "")
    if not relay_server_secret:
        raise ValueError("RELAY_SERVER_SECRET_KEY environment variable not set")


class OTELConfig:
    """OpenTelemetry configuration."""

    OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: str = ""
    OTEL_TRACES_EXPORTER: str = ""
    OTEL_RESOURCE_ATTRIBUTES: str = ""
    service_name = os.environ.get("OTEL_SERVICE_NAME", "rag-server")


class Module(Enum):
    """Supported module types."""

    events = "events"
    recommendations = "recommendations"
    prometheus = "prometheus"
    loki = "loki"
    planner = "planner"
    docs = "docs"
    kb = "knowledge_base"
    traces = "traces"
    nudgebee_docs = "nudgebee_docs"
