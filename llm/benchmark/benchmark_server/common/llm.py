import json
import logging
from typing import List, Dict

from langchain_aws import BedrockEmbeddings, SagemakerEndpoint, BedrockLLM
from langchain_aws.llms.sagemaker_endpoint import LLMContentHandler
from langchain_azure_ai.embeddings.inference import AzureAIEmbeddingsModel
from langchain_community.chat_models import AzureChatOpenAI
from langchain_community.embeddings import HuggingFaceInferenceAPIEmbeddings
from langchain_community.embeddings.sagemaker_endpoint import (
    EmbeddingsContentHandler,
    SagemakerEndpointEmbeddings,
)
from langchain_community.llms.huggingface_endpoint import HuggingFaceEndpoint
from langchain_core.embeddings import Embeddings
from langchain_core.language_models import BaseLLM
from langchain_google_genai import GoogleGenerativeAIEmbeddings, GoogleGenerativeAI
from langchain_google_vertexai import VertexAI
from langchain_google_vertexai import VertexAIEmbeddings
from langchain_ollama import OllamaEmbeddings
from langchain_openai import OpenAIEmbeddings, AzureOpenAIEmbeddings, ChatOpenAI
from pydantic import SecretStr

from benchmark_server.utils.utils import bedrock_client, Config

logger = logging.getLogger(__name__)

APPLICATION_JSON = "application/json"

	// added a test change which does absolutely nothing

def get_embeddings() -> Embeddings:
    embeddings: Embeddings
    if Config.embeddings_provider.lower() == "azure":
        embeddings = get_azure_embeddings()
    elif Config.embeddings_provider.lower() == "huggingface":
        embeddings = get_hf_embeddings()
    elif Config.embeddings_provider.lower() == "ollama":
        embeddings = get_ollama_embeddings()
    elif Config.embeddings_provider.lower() == "sagemaker":
        embeddings = get_sagemaker_embeddings()
    elif Config.embeddings_provider.lower() == "openai":
        embeddings = get_openai_embeddings()
    elif Config.embeddings_provider.lower() == "googleai":
        embeddings = get_google_ai_embeddings()
    elif Config.embeddings_provider.lower() == "vertexai":
        embeddings = get_vertexai_embeddings()
    else:
        embeddings = get_bedrock_embeddings()
    return embeddings


def get_google_ai_embeddings():
    if not Config.embeddings_model_id:
        raise ValueError(
            "Google AI embeddings provider requires EMBEDDINGS_MODEL_NAME in environment"
        )
    if not Config.embeddings_api_key:
        raise ValueError(
            "Google AI embeddings provider requires EMBEDDINGS_PROVIDER_API_KEY in environment"
        )
    # model format is models/embedding-001
    model = Config.embeddings_model_id
    if not model.startswith("models/"):
        model = f"models/{model}"
    return GoogleGenerativeAIEmbeddings(
        model=model, google_api_key=Config.embeddings_api_key
    )


def get_vertexai_embeddings():
    if not Config.embeddings_model_id:
        raise ValueError(
            "Vertex AI embeddings provider requires EMBEDDINGS_MODEL_NAME in environment"
        )
    if not Config.embeddings_region:
        raise ValueError(
            "Vertex AI embeddings provider requires EMBEDDINGS_PROVIDER_REGION in environment"
        )
    return VertexAIEmbeddings(
        model_name=Config.embeddings_model_id, location=Config.embeddings_region
    )


def get_bedrock_embeddings():
    if not Config.embeddings_model_id:
        raise ValueError(
            "Bedrock embeddings provider requires EMBEDDINGS_MODEL_NAME in environment"
        )
    return BedrockEmbeddings(
        client=bedrock_client(), model_id=Config.embeddings_model_id
    )


def get_azure_embeddings():
    if not Config.embeddings_api_endpoint:
        raise ValueError(
            "Azure embeddings provider requires EMBEDDINGS_PROVIDER_API_ENDPOINT in environment"
        )
    if not Config.embeddings_api_key:
        raise ValueError(
            "Azure embeddings provider requires EMBEDDINGS_PROVIDER_API_KEY in environment"
        )
    if not Config.embeddings_model_id:
        raise ValueError(
            "Azure embeddings provider requires EMBEDDINGS_MODEL_NAME in environment"
        )
    return AzureAIEmbeddingsModel(
        endpoint=Config.embeddings_api_endpoint,
        credential=Config.embeddings_api_key,
        api_version=Config.embeddings_api_version,
    )


def get_ollama_embeddings():
    if not Config.embeddings_model_id:
        raise ValueError(
            "Ollama embeddings provider requires EMBEDDINGS_MODEL_NAME in environment"
        )
    if not Config.embeddings_api_endpoint:
        raise ValueError(
            "Ollama embeddings provider requires EMBEDDINGS_PROVIDER_API_ENDPOINT in environment"
        )
    return OllamaEmbeddings(
        model=Config.embeddings_model_id, base_url=Config.embeddings_api_endpoint
    )


def get_hf_embeddings():
    if not Config.embeddings_api_endpoint:
        raise ValueError(
            "Huggingface embeddings provider requires EMBEDDINGS_PROVIDER_API_ENDPOINT in environment"
        )
    if not Config.embeddings_api_key:
        raise ValueError(
            "Huggingface embeddings provider requires EMBEDDINGS_PROVIDER_API_KEY in environment"
        )
    return HuggingFaceInferenceAPIEmbeddings(
        api_key=SecretStr(Config.embeddings_api_key),
        api_url=Config.embeddings_api_endpoint,
    )


def get_sagemaker_embeddings():
    if not Config.embeddings_api_endpoint:
        raise ValueError(
            "Sagemaker embeddings provider requires EMBEDDINGS_PROVIDER_API_ENDPOINT in environment"
        )
    if not Config.embeddings_region:
        raise ValueError(
            "Sagemaker embeddings provider requires EMBEDDINGS_PROVIDER_REGION in environment"
        )
    content_handler = ContentHandler()
    return SagemakerEndpointEmbeddings(
        endpoint_name=Config.embeddings_api_endpoint,
        region_name=Config.embeddings_region,
        content_handler=content_handler,
    )


def get_openai_embeddings():
    if not Config.embeddings_api_key:
        raise ValueError(
            "OpenAI embeddings provider requires EMBEDDINGS_PROVIDER_API_KEY in environment"
        )
    if not Config.embeddings_model_id:
        raise ValueError(
            "OpenAI embeddings provider requires EMBEDDINGS_MODEL_NAME in environment"
        )
    if not Config.embeddings_api_version:
        raise ValueError(
            "OpenAI embeddings provider requires EMBEDDINGS_PROVIDER_API_VERSION in environment"
        )
    if not Config.embeddings_api_type:
        raise ValueError(
            "OpenAI embeddings provider requires EMBEDDINGS_PROVIDER_API_TYPE in environment"
        )
    if Config.embeddings_api_type == "azure":
        if not Config.embeddings_api_endpoint:
            raise ValueError(
                "Azure OpenAI embeddings provider requires EMBEDDINGS_PROVIDER_API_ENDPOINT in environment"
            )
        embeddings = AzureOpenAIEmbeddings(
            model=Config.embeddings_model_id,
            api_key=Config.embeddings_api_key,
            api_version=Config.embeddings_api_version,
            openai_api_type=Config.embeddings_api_type,
            azure_endpoint=Config.embeddings_api_endpoint,
        )
    else:
        embeddings = OpenAIEmbeddings(
            api_key=Config.embeddings_api_key,
            model=Config.embeddings_model_id,
            api_version=Config.embeddings_api_version,
        )
    return embeddings


class ContentHandler(EmbeddingsContentHandler):
    content_type = APPLICATION_JSON
    accepts = APPLICATION_JSON

    def transform_input(self, inputs: list[str], model_kwargs: Dict) -> bytes:
        input_str = json.dumps({"inputs": inputs, **model_kwargs})
        return input_str.encode("utf-8")

    def transform_output(self, output: bytes) -> List[List[float]]:
        response_json = json.loads(output.decode("utf-8"))
        vectors = response_json.get("vectors")
        if not isinstance(vectors, list):
            raise ValueError("Expected 'vectors' to be a list of lists of floats")
        return vectors


class SageMakerContentHandler(LLMContentHandler):
    content_type = APPLICATION_JSON
    accepts = APPLICATION_JSON

    def transform_input(self, prompt: str, model_kwargs: Dict) -> bytes:
        input_str = json.dumps({prompt: prompt, **model_kwargs})
        return input_str.encode("utf-8")

    def transform_output(self, output: bytes) -> str:
        response_json = json.loads(output.decode("utf-8"))
        return str(response_json[0]["generated_text"])


def get_bedrock_llm() -> BedrockLLM:
    return BedrockLLM(
        model=Config.llm_model_name,
        region=Config.aws_region,
        client=bedrock_client(),
        streaming=True,
        model_kwargs={
            "temperature": 0.3,
            "top_p": 0.9,
            "max_gen_len": 1024,
        },
    )


def get_vertexai_llm(model_name: str) -> VertexAI:
    if not Config.llm_provider_api_key:
        logger.error("LLM_PROVIDER_API_KEY environment variable is not set")
    return VertexAI(model_name=model_name, temperature=0, streaming=True)


def get_google_ai_llm(model_name: str) -> GoogleGenerativeAI:
    if not Config.llm_provider_api_key:
        logger.error("LLM_PROVIDER_API_KEY environment variable is not set")
    return GoogleGenerativeAI(
        model=model_name,
        api_key=SecretStr(Config.llm_provider_api_key),
        temperature=0,
    )


def get_azure_llm(model_name: str) -> AzureChatOpenAI:
    if not Config.llm_provider_api_key:
        logger.error(
            "LLM_PROVIDER_API_KEY environment variable is not set for Azure LLM provider."
        )
    return AzureChatOpenAI(
        api_key=Config.llm_provider_api_key,
        base_url=Config.llm_provider_api_endpoint,
        api_version=Config.llm_provider_api_version,
        azure_deployment=model_name,
        temperature=0,
        streaming=True,
    )


def get_hf_llm(model_name: str) -> HuggingFaceEndpoint:
    return HuggingFaceEndpoint(
        model=model_name,
        endpoint_url=Config.llm_provider_api_endpoint,
        huggingfacehub_api_token=Config.llm_provider_api_key,
        task="text-generation",
        streaming=True,
    )


def get_sagemaker_llm() -> SagemakerEndpoint:
    content_handler = SageMakerContentHandler()
    return SagemakerEndpoint(
        endpoint_name=Config.llm_provider_api_endpoint,
        region_name=Config.llm_provider_region,
        content_handler=content_handler,
        streaming=True,
    )


def get_openai_llm(model_name: str) -> ChatOpenAI:
    if not Config.llm_provider_api_key:
        logger.error("LLM_PROVIDER_API_KEY is not set for OpenAI provider.")
    return ChatOpenAI(
        model=model_name,
        api_key=SecretStr(Config.llm_provider_api_key),
        base_url=(
            Config.llm_provider_api_endpoint
            if hasattr(Config, "openai_api_type")
            and Config.llm_provider_api_type.lower() != "openai"
            else None
        ),
        temperature=0,
        streaming=True,
    )


def get_llm() -> BaseLLM | AzureChatOpenAI | ChatOpenAI:
    llm: BaseLLM | AzureChatOpenAI | ChatOpenAI
    provider = Config.llm_provider.lower()
    if provider == "azure":
        llm = get_azure_llm(Config.llm_model_name)
    elif provider == "openai":
        llm = get_openai_llm(Config.llm_model_name)
    elif provider == "googleai":
        llm = get_google_ai_llm(Config.llm_model_name)
    elif provider == "vertexai":
        llm = get_vertexai_llm(Config.llm_model_name)
    elif provider == "sagemaker":
        llm = get_sagemaker_llm()
    elif provider == "huggingface":
        llm = get_hf_llm(Config.llm_model_name)
    else:
        llm = get_bedrock_llm()

    return llm
