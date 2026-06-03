"""Direct embedding providers replacing langchain wrappers.

Each provider implements the Embeddings ABC using native SDK calls.
"""

import json
import logging
from typing import Any, Dict, List, Optional

from rag.core.types import Embeddings

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Mixin: periodic client refresh for httpx-backed SDKs
# ---------------------------------------------------------------------------
# The OpenAI, Azure, and Google genai SDKs all use httpx internally.
# During bulk embedding (10K+ documents), the httpx connection pool
# accumulates response data that Python's GC cannot reclaim due to
# cyclic references.  Recreating the client every N calls flushes
# the pool and bounds memory growth.
# ---------------------------------------------------------------------------

_CLIENT_REFRESH_INTERVAL = 500  # recreate httpx-backed client every N embed calls


class _HttpxClientRefreshMixin:
    """Mixin that periodically recreates ``self._client`` via ``_create_client()``.

    Subclasses MUST initialise ``self._call_count = 0`` in ``__init__``. No
    class-level default is provided on purpose: a shared class attribute would
    silently become a cross-instance counter if a future subclass forgets the
    instance-level assignment.
    """

    def _maybe_refresh_client(self):
        self._call_count += 1
        if self._call_count % _CLIENT_REFRESH_INTERVAL != 0:
            return

        logger.info(
            f"[{type(self).__name__}] Refreshing client after {self._call_count} calls "
            "to release httpx connection pool memory"
        )
        old_client = getattr(self, "_client", None)
        try:
            self._create_client()
        except Exception:
            # Keep serving with the existing client; retry on the next call
            # rather than waiting another _CLIENT_REFRESH_INTERVAL calls.
            logger.exception(f"[{type(self).__name__}] Failed to refresh client; keeping existing client")
            self._call_count -= 1
            return

        # Release the old client's httpx connection pool immediately instead of
        # waiting on GC — waiting on GC is the original bug this mixin fixes.
        close_fn = getattr(old_client, "close", None)
        if callable(close_fn):
            try:
                close_fn()
            except Exception:
                logger.debug(
                    f"[{type(self).__name__}] old client .close() raised; ignoring",
                    exc_info=True,
                )


class OpenAIEmbeddings(_HttpxClientRefreshMixin, Embeddings):
    """OpenAI embeddings using the openai SDK directly."""

    def __init__(self, api_key: str, model: str, api_version: str = "", base_url: Optional[str] = None):
        self._api_key = api_key
        self._base_url = base_url
        self.model = model
        self.model_id = model
        self._call_count = 0
        self._create_client()

    def _create_client(self):
        import openai

        kwargs: Dict[str, Any] = {"api_key": self._api_key}
        if self._base_url:
            kwargs["base_url"] = self._base_url
        self._client = openai.OpenAI(**kwargs)

    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        self._maybe_refresh_client()
        resp = self._client.embeddings.create(input=texts, model=self.model)
        return [item.embedding for item in resp.data]

    def embed_query(self, text: str) -> List[float]:
        return self.embed_documents([text])[0]


class AzureOpenAIEmbeddings(_HttpxClientRefreshMixin, Embeddings):
    """Azure OpenAI embeddings using the openai SDK with azure config."""

    def __init__(self, api_key: str, model: str, api_version: str, azure_endpoint: str, **kwargs: Any):
        self._api_key = api_key
        self._api_version = api_version
        self._azure_endpoint = azure_endpoint
        self.model = model
        self.model_id = model
        self._call_count = 0
        self._create_client()

    def _create_client(self):
        import openai

        self._client = openai.AzureOpenAI(
            api_key=self._api_key,
            api_version=self._api_version,
            azure_endpoint=self._azure_endpoint,
        )

    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        self._maybe_refresh_client()
        resp = self._client.embeddings.create(input=texts, model=self.model)
        return [item.embedding for item in resp.data]

    def embed_query(self, text: str) -> List[float]:
        return self.embed_documents([text])[0]


class AzureAIEmbeddings(_HttpxClientRefreshMixin, Embeddings):
    """Azure AI embeddings using azure-ai-inference SDK."""

    def __init__(self, endpoint: str, credential: str, model_name: str, api_version: str = ""):
        self._endpoint = endpoint
        self._credential = credential
        self.model = model_name
        self.model_id = model_name
        self._call_count = 0
        self._create_client()

    def _create_client(self):
        from azure.ai.inference import EmbeddingsClient
        from azure.core.credentials import AzureKeyCredential

        self._client = EmbeddingsClient(endpoint=self._endpoint, credential=AzureKeyCredential(self._credential))

    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        self._maybe_refresh_client()
        resp = self._client.embed(input=texts, model=self.model)
        return [[float(x) for x in item.embedding] for item in resp.data]

    def embed_query(self, text: str) -> List[float]:
        return self.embed_documents([text])[0]


class GoogleGenAIEmbeddings(_HttpxClientRefreshMixin, Embeddings):
    """Google Generative AI embeddings using google-genai SDK."""

    def __init__(self, model: str, api_key: str):
        self._api_key = api_key
        self.model = model
        self.model_id = model
        self._call_count = 0
        self._create_client()

    def _create_client(self):
        from google import genai

        self._client = genai.Client(api_key=self._api_key)

    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        self._maybe_refresh_client()
        result = self._client.models.embed_content(model=self.model, contents=texts)
        embeddings = result.embeddings or []
        return [[float(x) for x in (e.values or [])] for e in embeddings]

    def embed_query(self, text: str) -> List[float]:
        return self.embed_documents([text])[0]


class VertexAIEmbeddings(_HttpxClientRefreshMixin, Embeddings):
    """Google Vertex AI embeddings using google-genai SDK with vertexai=True."""

    def __init__(self, model_name: str, location: str):
        self._location = location
        self.model = model_name
        self.model_id = model_name
        self._call_count = 0
        self._create_client()

    def _create_client(self):
        from google import genai

        self._client = genai.Client(vertexai=True, location=self._location)

    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        self._maybe_refresh_client()
        result = self._client.models.embed_content(model=self.model, contents=texts)
        embeddings = result.embeddings or []
        return [[float(x) for x in (e.values or [])] for e in embeddings]

    def embed_query(self, text: str) -> List[float]:
        return self.embed_documents([text])[0]


class BedrockEmbeddings(Embeddings):
    """AWS Bedrock embeddings using boto3."""

    def __init__(self, client: Any, model_id: str):
        self._client = client
        self.model = model_id
        self.model_id = model_id

    def _embed_single(self, text: str) -> List[float]:
        body = json.dumps({"inputText": text})
        resp = self._client.invoke_model(modelId=self.model_id, body=body, contentType="application/json")
        result = json.loads(resp["body"].read())
        embedding: List[float] = result["embedding"]
        return embedding

    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        return [self._embed_single(t) for t in texts]

    def embed_query(self, text: str) -> List[float]:
        return self._embed_single(text)


class SagemakerEndpointEmbeddings(Embeddings):
    """AWS SageMaker endpoint embeddings."""

    def __init__(self, endpoint_name: str, region_name: str):
        import boto3

        self._client = boto3.client("sagemaker-runtime", region_name=region_name)
        self.endpoint_name = endpoint_name
        self.model = endpoint_name
        self.model_id = endpoint_name

    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        body = json.dumps({"inputs": texts})
        resp = self._client.invoke_endpoint(
            EndpointName=self.endpoint_name,
            ContentType="application/json",
            Body=body.encode("utf-8"),
        )
        result = json.loads(resp["Body"].read().decode("utf-8"))
        vectors = result.get("vectors", result)
        if not isinstance(vectors, list):
            raise ValueError("Expected 'vectors' to be a list of lists of floats")
        return vectors

    def embed_query(self, text: str) -> List[float]:
        return self.embed_documents([text])[0]


class HuggingFaceInferenceAPIEmbeddings(Embeddings):
    """HuggingFace Inference API embeddings using requests."""

    def __init__(self, api_key: str, api_url: str):
        self._api_key = api_key
        self._api_url = api_url
        self.model = api_url
        self.model_id = api_url

    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        import requests

        headers = {"Authorization": f"Bearer {self._api_key}"}
        resp = requests.post(self._api_url, headers=headers, json={"inputs": texts}, timeout=120)
        resp.raise_for_status()
        result: List[List[float]] = resp.json()
        return result

    def embed_query(self, text: str) -> List[float]:
        return self.embed_documents([text])[0]


class OllamaEmbeddings(Embeddings):
    """Ollama embeddings using HTTP API."""

    def __init__(self, model: str, base_url: str):
        self.model = model
        self.model_id = model
        self._base_url = base_url.rstrip("/")

    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        import requests

        results = []
        for text in texts:
            resp = requests.post(
                f"{self._base_url}/api/embed",
                json={"model": self.model, "input": text},
                timeout=120,
            )
            resp.raise_for_status()
            data = resp.json()
            results.append(data["embeddings"][0] if "embeddings" in data else data["embedding"])
        return results

    def embed_query(self, text: str) -> List[float]:
        return self.embed_documents([text])[0]
