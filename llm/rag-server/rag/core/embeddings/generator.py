import logging
import threading
import time
from typing import Any, Dict, Optional, Callable, Tuple

from rag.core.embeddings.providers import (
    AzureAIEmbeddings,
    AzureOpenAIEmbeddings,
    BedrockEmbeddings,
    GoogleGenAIEmbeddings,
    HuggingFaceInferenceAPIEmbeddings,
    OllamaEmbeddings,
    OpenAIEmbeddings,
    SagemakerEndpointEmbeddings,
    VertexAIEmbeddings,
)
from rag.core.types import LLM, Embeddings
from rag.core.utils.db_query import get_llm_integrations, get_embeddings_integrations
from utils.config import Config
from utils.shared import bedrock_client

logger = logging.getLogger(__name__)

# Instance caches for embeddings and LLM clients (keyed by account_id)
_embeddings_instance_cache: Dict[str, Dict[str, Any]] = {}
_llm_instance_cache: Dict[str, Dict[str, Any]] = {}
_instance_cache_lock = threading.Lock()

# Instance caches for embeddings and LLM clients (keyed by account_id)
_embeddings_instance_cache: Dict[str, Dict[str, Any]] = {}
_llm_instance_cache: Dict[str, Dict[str, Any]] = {}
_instance_cache_lock = threading.Lock()


def validate_embeddings_provider_keys(selected_provider: str, get_config_value) -> None:
    required_keys = {
        "azure": ["embeddings_api_key", "embeddings_api_endpoint", "embeddings_api_version", "embeddings_model_id"],
        "huggingface": ["embeddings_api_key", "embeddings_api_endpoint", "embeddings_model_id"],
        "ollama": ["embeddings_model_id", "embeddings_api_endpoint"],
        "sagemaker": ["embeddings_api_endpoint", "embeddings_region"],
        "openai": ["embeddings_api_key", "embeddings_model_id", "embeddings_api_version"],
        "googleai": ["embeddings_api_key", "embeddings_model_id"],
        "vertexai": ["embeddings_model_id", "embeddings_region"],
        "ondevice": [],
    }
    if selected_provider in required_keys:
        missing = [k for k in required_keys[selected_provider] if not get_config_value(k)]
        if missing:
            raise ValueError(
                f"Missing required config values for embeddings provider '{selected_provider}': {', '.join(missing)}"
            )


def ondevice() -> Embeddings:
    from rag.core.embeddings.local import OnDeviceEmbeddings

    return OnDeviceEmbeddings()


def build_embeddings_provider_map(
    get_config_value: Callable[[str, Optional[Any]], Any], bedrock_client_fn: Callable[[], Any]
) -> Tuple[Dict[str, Callable[[], Embeddings]], Callable[[], Embeddings]]:
    def azure() -> Embeddings:
        return AzureAIEmbeddings(
            endpoint=str(get_config_value("embeddings_api_endpoint", None)),
            credential=str(get_config_value("embeddings_api_key", None)),
            model_name=str(get_config_value("embeddings_model_id", None)),
            api_version=str(get_config_value("embeddings_api_version", None)),
        )

    def huggingface() -> Embeddings:
        api_key = get_config_value("embeddings_api_key", None)
        if not isinstance(api_key, str):
            raise ValueError("embeddings_api_key must be a string for HuggingFaceInferenceAPIEmbeddings")
        return HuggingFaceInferenceAPIEmbeddings(
            api_key=api_key,
            api_url=str(get_config_value("embeddings_api_endpoint", None)),
        )

    def ollama() -> Embeddings:
        return OllamaEmbeddings(
            model=str(get_config_value("embeddings_model_id", None)),
            base_url=str(get_config_value("embeddings_api_endpoint", None)),
        )

    def sagemaker() -> Embeddings:
        return SagemakerEndpointEmbeddings(
            endpoint_name=str(get_config_value("embeddings_api_endpoint", None)),
            region_name=str(get_config_value("embeddings_region", None)),
        )

    def openai() -> Embeddings:
        api_type = get_config_value("embeddings_api_type", None)
        api_key = str(get_config_value("embeddings_api_key", None))
        if api_type == "azure":
            return AzureOpenAIEmbeddings(
                model=str(get_config_value("embeddings_model_id", None)),
                api_key=api_key,
                api_version=str(get_config_value("embeddings_api_version", None)),
                azure_endpoint=str(get_config_value("embeddings_api_endpoint", None)),
            )
        return OpenAIEmbeddings(
            api_key=api_key,
            model=str(get_config_value("embeddings_model_id", None)),
            api_version=str(get_config_value("embeddings_api_version", None)),
        )

    def googleai() -> Embeddings:
        model = get_config_value("embeddings_model_id", None)
        if model and isinstance(model, str) and not model.startswith("models/"):
            model = f"models/{model}"
        return GoogleGenAIEmbeddings(
            model=str(model),
            api_key=str(get_config_value("embeddings_api_key", None)),
        )

    def vertexai() -> Embeddings:
        return VertexAIEmbeddings(
            model=str(get_config_value("embeddings_model_id", None)),
            location=str(get_config_value("embeddings_region", None)),
        )

    def bedrock() -> Embeddings:
        return BedrockEmbeddings(
            client=bedrock_client_fn(),
            model_id=str(get_config_value("embeddings_model_id", None)),
        )

    provider_map: Dict[str, Callable[[], Embeddings]] = {
        "azure": azure,
        "huggingface": huggingface,
        "ollama": ollama,
        "sagemaker": sagemaker,
        "openai": openai,
        "googleai": googleai,
        "vertexai": vertexai,
        "ondevice": ondevice,
    }
    return provider_map, bedrock


def get_embeddings(cloud_account_id: str) -> Embeddings:
    cache_key = cloud_account_id or "_default"
    now = time.time()

    with _instance_cache_lock:
        cached = _embeddings_instance_cache.get(cache_key)
        if cached and now - cached["time"] < Config.rag_llm_provider_cache_ttl:
            instance: Embeddings = cached["instance"]
            return instance

    integration_config: Optional[Dict[str, Any]] = None
    if cloud_account_id:
        integrations = get_embeddings_integrations(cloud_account_id)
        if integrations:
            integration_config = integrations[0].get("config", {})

    def get_config_value(key: str, default: Optional[Any] = None) -> Any:
        if integration_config and key in integration_config:
            return integration_config[key]
        return getattr(Config, key, default)

    provider: str = get_config_value("embeddings_provider", getattr(Config, "embeddings_provider", ""))
    provider = provider.lower() if isinstance(provider, str) else ""
    validate_embeddings_provider_keys(provider, get_config_value)
    provider_map, bedrock = build_embeddings_provider_map(get_config_value, bedrock_client)
    embeddings: Embeddings = provider_map.get(provider, bedrock)()
    if not isinstance(embeddings, Embeddings):
        raise TypeError(f"Embeddings provider '{provider}' did not return a valid Embeddings instance.")

    with _instance_cache_lock:
        _embeddings_instance_cache[cache_key] = {"instance": embeddings, "time": now}

    return embeddings


# --- LLM providers (direct SDK calls, no langchain) ---


def _get_str_config(config: Optional[dict], key: str, default: str = "") -> str:
    """Get a string config value from integration config or global Config."""
    if config and key in config:
        val = config[key]
        return val if isinstance(val, str) else ""
    return str(getattr(Config, key, default) or "")


def get_bedrock_llm() -> LLM:
    from rag.core.llm.providers import BedrockLLM

    return BedrockLLM(client=bedrock_client(), model=Config.llm_model_name)


def get_openai_llm(model_name: str, config: Optional[dict] = None) -> LLM:
    from rag.core.llm.providers import OpenAILLM

    api_key = _get_str_config(config, "llm_provider_api_key")
    api_type = _get_str_config(config, "llm_provider_api_type")
    api_endpoint = _get_str_config(config, "llm_provider_api_endpoint")
    base_url = api_endpoint if api_type and api_type.lower() != "openai" else None
    return OpenAILLM(model=model_name, api_key=api_key, base_url=base_url)


def get_azure_llm(model_name: str, config: Optional[dict] = None) -> LLM:
    from rag.core.llm.providers import AzureOpenAILLM

    return AzureOpenAILLM(
        model=model_name,
        api_key=_get_str_config(config, "llm_provider_api_key"),
        api_version=_get_str_config(config, "llm_provider_api_version"),
        azure_endpoint=_get_str_config(config, "llm_provider_api_endpoint"),
    )


def get_anthropic_llm(model_name: str, config: Optional[dict] = None) -> LLM:
    from rag.core.llm.providers import AnthropicLLM

    return AnthropicLLM(model=model_name, api_key=_get_str_config(config, "llm_provider_api_key"))


def get_google_ai_llm(model_name: str, config: Optional[dict] = None) -> LLM:
    from rag.core.llm.providers import GoogleGenAILLM

    return GoogleGenAILLM(model=model_name, api_key=_get_str_config(config, "llm_provider_api_key"))


def get_vertexai_llm(model_name: str, config: Optional[dict] = None) -> LLM:
    from rag.core.llm.providers import GoogleGenAILLM

    return GoogleGenAILLM(model=model_name, api_key=_get_str_config(config, "llm_provider_api_key"))


def get_hf_llm(model_name: str, config: Optional[dict] = None) -> LLM:
    from rag.core.llm.providers import HuggingFaceLLM

    return HuggingFaceLLM(
        model=model_name,
        endpoint_url=_get_str_config(config, "llm_provider_api_endpoint"),
        api_key=_get_str_config(config, "llm_provider_api_key"),
    )


def get_sagemaker_llm(config: Optional[dict] = None) -> LLM:
    from rag.core.llm.providers import SageMakerLLM

    return SageMakerLLM(
        endpoint_name=_get_str_config(config, "llm_provider_api_endpoint"),
        region_name=_get_str_config(config, "llm_provider_region"),
    )


def validate_llm_provider_keys(selected_provider: str, get_config_value: Callable) -> None:
    """Validate that required config keys are present for the selected LLM provider."""
    required_keys: Dict[str, list] = {
        "azure": [
            "llm_provider_api_key",
            "llm_provider_api_endpoint",
            "llm_provider_api_version",
            "llm_model_name",
        ],
        "openai": ["llm_provider_api_key", "llm_model_name"],
        "googleai": ["llm_provider_api_key", "llm_model_name"],
        "vertexai": ["llm_provider_api_key", "llm_model_name"],
        "sagemaker": ["llm_provider_api_endpoint", "llm_provider_region"],
        "huggingface": ["llm_provider_api_key", "llm_provider_api_endpoint", "llm_model_name"],
        "anthropic": ["llm_provider_api_key", "llm_model_name"],
    }
    if selected_provider in required_keys:
        missing = [k for k in required_keys[selected_provider] if not get_config_value(k)]
        if missing:
            raise ValueError(f"Missing required config values for provider '{selected_provider}': {', '.join(missing)}")


def build_llm_provider_map(
    model_name: str, integration_config: Optional[dict]
) -> Tuple[Dict[str, Callable[[], LLM]], Callable[[], LLM]]:
    """Build a map of LLM provider name to factory function."""
    return {
        "azure": lambda: get_azure_llm(model_name, integration_config),
        "openai": lambda: get_openai_llm(model_name, integration_config),
        "googleai": lambda: get_google_ai_llm(model_name, integration_config),
        "vertexai": lambda: get_vertexai_llm(model_name, integration_config),
        "sagemaker": lambda: get_sagemaker_llm(integration_config),
        "huggingface": lambda: get_hf_llm(model_name, integration_config),
        "anthropic": lambda: get_anthropic_llm(model_name, integration_config),
    }, get_bedrock_llm


def get_llm(cloud_account_id: str) -> LLM:
    cache_key = cloud_account_id or "_default"
    now = time.time()

    with _instance_cache_lock:
        cached = _llm_instance_cache.get(cache_key)
        if cached and now - cached["time"] < Config.rag_llm_provider_cache_ttl:
            instance: LLM = cached["instance"]
            return instance

    integration_config = None
    if cloud_account_id:
        integrations = get_llm_integrations(cloud_account_id)
        if integrations:
            integration_config = integrations[0].get("config", {})

    def get_config_value(key: str, default: Optional[Any] = None) -> Any:
        if integration_config and key in integration_config:
            return integration_config[key]
        return getattr(Config, key, default)

    provider = get_config_value("llm_provider", getattr(Config, "llm_provider", ""))
    provider = provider.lower() if isinstance(provider, str) else ""
    model_name = get_config_value("llm_model_name", getattr(Config, "llm_model_name", ""))
    validate_llm_provider_keys(provider, get_config_value)
    provider_map, bedrock = build_llm_provider_map(model_name, integration_config)
    llm = provider_map.get(provider, bedrock)()
    logger.info(f"Using LLM provider: {provider} with model: {model_name}")

    with _instance_cache_lock:
        _llm_instance_cache[cache_key] = {"instance": llm, "time": now}

    return llm
