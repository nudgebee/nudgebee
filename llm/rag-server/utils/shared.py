"""
Shared utility functions for RAG server.

Contains helper functions for tracing, AWS clients, HTTP sessions, and file locking.
"""

import json
import logging
import os

import boto3
import requests
from filelock import BaseFileLock
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import ConsoleSpanExporter, SimpleSpanProcessor
from requests.adapters import HTTPAdapter
from urllib3 import Retry

from utils.config import Config, OTELConfig

logger = logging.getLogger(__name__)


def get_trace_provider(service_name: str):
    """
    Create and configure an OpenTelemetry trace provider.

    Args:
        service_name: Name of the service for tracing

    Returns:
        TracerProvider instance
    """
    # fetching resource attr else returning empty dict
    resource_attr = (
        {
            key.strip(): str(value.strip())
            for key, value in (item.split("=") for item in OTELConfig.OTEL_RESOURCE_ATTRIBUTES.split(","))
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
        otlp_exporter = OTLPSpanExporter(endpoint=OTELConfig.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, timeout=5)
        provider.add_span_processor(SimpleSpanProcessor(otlp_exporter))
    if "console" in exporters:
        console_exporter = ConsoleSpanExporter(formatter=lambda span: json.dumps(json.loads(span.to_json(indent=0))))
        provider.add_span_processor(SimpleSpanProcessor(console_exporter))
    return provider


def set_global_trace():
    """Set the global trace provider for OpenTelemetry."""
    trace.set_tracer_provider(get_trace_provider(OTELConfig.service_name))


def bedrock_client():
    """
    Create and return an AWS Bedrock runtime client.

    Returns:
        Boto3 Bedrock runtime client
    """
    return boto3.client("bedrock-runtime", region_name=Config.aws_region)


def s3_client():
    """
    Create and return an AWS S3 client.

    Returns:
        Boto3 S3 client
    """
    return boto3.client("s3", region_name=Config.aws_region)


PROVIDER_NAME_MAP = {
    # LLMs
    "OpenAILLM": "openai",
    "AzureOpenAILLM": "azure",
    "AnthropicLLM": "anthropic",
    "GoogleGenAILLM": "googleai",
    "BedrockLLM": "bedrock",
    "HuggingFaceLLM": "huggingface",
    "SageMakerLLM": "sagemaker",
    # Embeddings
    "OpenAIEmbeddings": "openai",
    "AzureOpenAIEmbeddings": "azure",
    "AzureAIEmbeddings": "azure",
    "GoogleGenAIEmbeddings": "googleai",
    "VertexAIEmbeddings": "vertexai",
    "BedrockEmbeddings": "bedrock",
    "HuggingFaceInferenceAPIEmbeddings": "huggingface",
    "OllamaEmbeddings": "ollama",
    "SagemakerEndpointEmbeddings": "sagemaker",
    "OnDeviceEmbeddings": "ondevice",
}


def get_provider_name(class_name: str) -> str:
    """
    Maps a LangChain class name to a standardized provider name.

    Args:
        class_name: LangChain class name (e.g., "ChatOpenAI", "BedrockEmbeddings")

    Returns:
        Standardized provider name (e.g., "openai", "bedrock")
    """
    return PROVIDER_NAME_MAP.get(class_name, class_name)


def get_http_session_with_retry():
    """
    Create HTTP session with retry strategy.

    Returns:
        requests.Session with retry configuration
    """
    # Configure retry settings for HTTP requests
    retry_strategy = Retry(
        total=Config.max_retry_count,  # Total number of retries
        status_forcelist=[429, 500, 502, 503, 504],  # Retry on these HTTP status codes
        allowed_methods=["HEAD", "GET", "OPTIONS", "POST"],  # Only retry on these HTTP methods
        backoff_factor=1,  # Backoff factor for delay between retries
    )
    # Create a session with the retry strategy
    session = requests.Session()
    session.mount("https://", HTTPAdapter(max_retries=retry_strategy))
    session.mount("http://", HTTPAdapter(max_retries=retry_strategy))
    return session


def get_collection_name(account_id: str | None, module: str | None) -> str | None:
    """
    Generate collection name from account_id and module.

    Args:
        account_id: Account identifier
        module: Module name

    Returns:
        Collection name string or None
    """
    collection_name = None
    if account_id:
        collection_name = str(account_id)
    if module:
        collection_name = f"{account_id}_{module}" if collection_name else module
    return collection_name


def release_lock(lock: BaseFileLock, lock_path: str, module: str) -> None:
    """
    Release and cleanup file lock.

    Args:
        lock: FileLock instance
        lock_path: Path to lock file
        module: Module name for logging
    """
    try:
        if os.path.exists(lock_path):
            lock.release(force=True)
            logger.info(f"Lock released for module: {module}")
    except Exception as e:
        logger.warning(f"Failed to releasing lock: {e}")
    finally:
        if os.path.exists(lock_path):
            try:
                os.remove(lock_path)
                logger.info(f"Lock file removed: {lock_path}")
            except Exception as rm_error:
                logger.error(f"Failed to remove lock file: {rm_error}")
