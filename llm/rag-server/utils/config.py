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
    # Benchmarked on 39 real queries from conversation history (29 needing live
    # data, 10 answerable from the KB):
    #
    #   model                  errors/39   latency (8 docs, 2 threads)
    #   bge-reranker-v2-m3     0           1480ms  @ max_length 256
    #   bge-reranker-base      1            362ms
    #   ms-marco-MiniLM-L-6    1            309ms
    #
    # v2-m3 was the only model that separated the two sets cleanly (irrelevant
    # topped out at 0.775, relevant scored 0.900+); MiniLM's scores overlap.
    # The defaults below are MiniLM, chosen for latency and footprint — v2-m3
    # cost ~1.1GB resident and reranked 16 documents in 6-61s in prod. The
    # accuracy cost of that swap at threshold 0.50 / max_length 128 has not been
    # measured against the table above.
    reranker_model = os.environ.get("RAG_RERANKER_MODEL", "cross-encoder/ms-marco-MiniLM-L-6-v2")
    # Minimum 0-1 relevance for a document to survive. A property of the model,
    # not of the corpus — retune when the model changes.
    reranker_threshold = float(os.environ.get("RAG_RERANKER_THRESHOLD", 0.50))
    reranker_max_length = int(os.environ.get("RAG_RERANKER_MAX_LENGTH", 128))  # tokens per (query, doc) pair
    reranker_max_doc_chars = int(os.environ.get("RAG_RERANKER_MAX_DOC_CHARS", 500))  # doc chars scored
    # int8 quantization: measured slower and larger on ARM (qnnpack), so it is
    # off until measured to help on the target platform (x86/fbgemm).
    reranker_quantize = os.environ.get("RAG_RERANKER_QUANTIZE", "false").lower() == "true"
    # Weights dtype: float32 or bfloat16. bfloat16 halves resident weights and
    # engages the AMX-BF16 path on the deployment nodes, which fp32 never
    # touches. Opt-in until its speed is measured there. float16 is deliberately
    # not offered: it runs on these nodes, but bf16 is what AMX accelerates and
    # carries the wider exponent range, so fp16 would add a choice with no use.
    reranker_dtype = os.environ.get("RAG_RERANKER_DTYPE", "float32").strip().lower()
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
