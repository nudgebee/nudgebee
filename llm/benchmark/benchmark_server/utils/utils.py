import json
import logging
import os

import boto3
import requests
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import ConsoleSpanExporter, SimpleSpanProcessor
from requests.adapters import HTTPAdapter
from urllib3 import Retry

logger = logging.getLogger(__name__)


def get_trace_provider(service_name: str):
    resource_attr = (
        {
            key.strip(): str(value.strip())
            for key, value in (
                item.split("=")
                for item in OTELConfig.OTEL_RESOURCE_ATTRIBUTES.split(",")
            )
        }
        if OTELConfig.OTEL_RESOURCE_ATTRIBUTES
        else {}
    )
    if service_name:
        resource_attr["service.name"] = service_name
        resource_attr["application"] = service_name
    otl_resource = Resource.create()
    provider = TracerProvider(resource=otl_resource)
    exporters = OTELConfig.OTEL_TRACES_EXPORTER.split(",")
    if "otlp" in exporters:
        if not OTELConfig.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:
            raise ValueError(
                "OTLP exporter(OTEL_TRACES_EXPORTER) is enabled but OTLP trace "
                "endpoint(OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) is not provided"
            )
        otlp_exporter = OTLPSpanExporter(
            endpoint=OTELConfig.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, timeout=5
        )
        provider.add_span_processor(SimpleSpanProcessor(otlp_exporter))
    if "console" in exporters:
        console_exporter = ConsoleSpanExporter(
            formatter=lambda span: json.dumps(json.loads(span.to_json(indent=0)))
        )
        provider.add_span_processor(SimpleSpanProcessor(console_exporter))
    return provider


def set_global_trace():
    trace.set_tracer_provider(get_trace_provider(OTELConfig.service_name))


def get_trace(name: str):
    return trace.get_tracer(name)


class Config:
    # General
    os.environ["TOKENIZERS_PARALLELISM"] = "true"
    max_token_length = int(os.environ.get("MAX_TOKEN_LENGTH", 8192))
    embedding_batch_size = int(os.environ.get("EMBEDDING_BATCH_SIZE", 10))
    max_concurrency = int(os.environ.get("MAX_CONCURRENCY", 2))
    max_retry_count = int(os.environ.get("MAX_RETRY_COUNT", 5))
    max_retry_duration = int(os.environ.get("MAX_RETRY_DURATION", 600))
    initial_wait_time = int(os.environ.get("INITIAL_WAIT_TIME", 1))

    # Embeddings
    embeddings_provider = os.environ.get("EMBEDDINGS_PROVIDER", "bedrock")
    # Model Name for different embeddings providers
    # Bedrock - 'amazon.titan-embed-text-v2:0', Azure - 'text-embedding-ada-002',
    # Huggingface - 'text-embedding-ada-002', Ollama - 'nb-text-embeddings', OpenAI - 'text-embedding-ada-002'
    embeddings_model_id = os.environ.get(
        "EMBEDDINGS_MODEL_NAME", "amazon.titan-embed-text-v2:0"
    )
    embeddings_api_endpoint = os.environ.get("EMBEDDINGS_PROVIDER_API_ENDPOINT", "")
    embeddings_api_key = os.environ.get("EMBEDDINGS_PROVIDER_API_KEY", "")
    embeddings_api_version = os.environ.get(
        "EMBEDDINGS_PROVIDER_API_VERSION", ""
    )  # Azure - '2023-05-15'
    embeddings_api_type = os.environ.get("EMBEDDINGS_PROVIDER_API_TYPE", "")
    embeddings_region = os.environ.get("EMBEDDINGS_PROVIDER_REGION", "us-west-2")

    # LLM
    # openai | aws_bedrock | sagemaker | azure | ollama | huggingface
    llm_provider = os.environ.get("LLM_PROVIDER", "bedrock")
    llm_model_name = os.environ.get("LLM_MODEL_NAME", "meta.llama3-1-70b-instruct-v1:0")
    llm_provider_api_endpoint = os.environ.get(
        "LLM_PROVIDER_API_ENDPOINT", ""
    )  # e.g., https://api.openai.com/v1
    llm_provider_api_key = os.environ.get("LLM_PROVIDER_API_KEY", "")
    llm_provider_api_version = os.environ.get(
        "LLM_PROVIDER_API_VERSION", ""
    )  # e.g., 2024-05-01-preview
    llm_provider_api_type = os.environ.get(
        "LLM_PROVIDER_API_TYPE", ""
    )  # e.g., openai | azure
    llm_provider_region = os.environ.get("LLM_PROVIDER_REGION", "us-west-2")

    # AWS Bedrock
    aws_region = os.environ.get("AWS_REGION", "us-west-2")
    aws_access_key = os.environ.get("aws_access_key_id", "")
    aws_secret_access_key = os.environ.get("aws_secret_access_key", "")

    # Email Config
    email_server_host = os.environ.get("EMAIL_SERVER_HOST", "smtp.gmail.com")
    email_server_port = int(os.environ.get("EMAIL_SERVER_PORT", 587))
    email_server_user = os.environ.get("EMAIL_SERVER_USER", "")
    email_server_password = os.environ.get("EMAIL_SERVER_PASSWORD", "")
    email_from = os.environ.get("EMAIL_FROM", "")

    # Target LLM Server
    llm_server_url = os.environ.get("LLM_SERVER_URL", "http://localhost:9999")


class OTELConfig:
    OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: str = ""
    OTEL_TRACES_EXPORTER: str = ""
    OTEL_RESOURCE_ATTRIBUTES: str = ""
    service_name = os.environ.get("OTEL_SERVICE_NAME", "llm-benchmark-server")


def get_http_session_with_retry():
    # Configure retry settings for HTTP requests
    retry_strategy = Retry(
        total=Config.max_retry_count,  # Total number of retries
        status_forcelist=[429, 500, 502, 503, 504],  # Retry on these HTTP status codes
        allowed_methods=[
            "HEAD",
            "GET",
            "OPTIONS",
            "POST",
        ],  # Only retry on these HTTP methods
        backoff_factor=1,  # Backoff factor for delay between retries
    )
    # Create a session with the retry strategy
    session = requests.Session()
    session.mount("https://", HTTPAdapter(max_retries=retry_strategy))
    session.mount("http://", HTTPAdapter(max_retries=retry_strategy))
    return session


def bedrock_client():
    return boto3.client("bedrock-runtime", region_name=Config.aws_region)
