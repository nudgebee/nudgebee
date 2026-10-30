"""Direct LLM providers replacing langchain wrappers.

Each provider implements the LLM ABC using native SDK calls.
Used exclusively for reranking — each provider sends a prompt and returns text + token usage.
"""

import base64
import json
import logging
import re
from typing import Any, Dict, List, Optional

from rag.core.types import LLM, LLMResult

logger = logging.getLogger(__name__)


def _ensure_user_content(prompt: str) -> str:
    """Guard against a request with no usable user query. Some chat templates (Qwen3 on
    vLLM) reject a message set whose only user turn is empty, so fall back to a minimal
    non-empty prompt. Mirrors llm-server's ensureUserMessage."""
    return prompt if prompt and prompt.strip() else "Continue."


def _user_message(content: Any) -> Any:
    """One user message carrying mixed text and image parts.

    Typed as Any on purpose: each SDK generates its own TypedDict union for message
    payloads, and a dict of content parts satisfies none of them cleanly. Contorting the
    wire shape to match a generated union would obscure what is actually sent.
    """
    return {"role": "user", "content": content}


def _openai_image_content(prompt: str, images: List[Dict[str, str]]) -> List[Dict[str, Any]]:
    """OpenAI-shaped multimodal content: the images, then the instruction."""
    content: List[Dict[str, Any]] = [
        {"type": "image_url", "image_url": {"url": f"data:{img['mime_type']};base64,{img['data']}"}} for img in images
    ]
    content.append({"type": "text", "text": _ensure_user_content(prompt)})
    return content


def _bedrock_image_format(mime_type: str) -> str:
    """Converse names the format, not the media type."""
    return {"image/png": "png", "image/jpeg": "jpeg", "image/gif": "gif", "image/webp": "webp"}.get(
        (mime_type or "").lower(), "png"
    )


# Reasoning models (o-series, gpt-5 family) reject a non-default temperature.
_REASONING_MODEL_RX = re.compile(r"(?:^|[^a-z0-9])(?:o[1-9]|gpt-5)", re.IGNORECASE)


def supports_vision(llm: Any) -> bool:
    """Whether this provider can be handed an image at all.

    A provider without the method can never describe a figure; one with it still
    depends on the configured model being able to see, which only the call itself
    can tell us.
    """
    return callable(getattr(llm, "generate_with_images", None))


class OpenAILLM(LLM):
    """OpenAI LLM using the openai SDK directly."""

    def __init__(self, model: str, api_key: str, base_url: Optional[str] = None):
        import openai

        kwargs: Dict[str, Any] = {"api_key": api_key}
        if base_url:
            kwargs["base_url"] = base_url
        self._client = openai.OpenAI(**kwargs)
        self.model = model

    def generate(self, prompt: str) -> LLMResult:
        resp = self._client.chat.completions.create(
            model=self.model,
            messages=[{"role": "user", "content": _ensure_user_content(prompt)}],
            temperature=0,
        )
        text = resp.choices[0].message.content or ""
        usage = resp.usage
        return LLMResult(
            text=text,
            input_tokens=usage.prompt_tokens if usage else 0,
            output_tokens=usage.completion_tokens if usage else 0,
        )

    def generate_with_images(self, prompt: str, images: List[Dict[str, str]]) -> LLMResult:
        resp = self._client.chat.completions.create(
            model=self.model,
            messages=[_user_message(_openai_image_content(prompt, images))],
            temperature=0,
        )
        text = resp.choices[0].message.content or ""
        usage = resp.usage
        return LLMResult(
            text=text,
            input_tokens=usage.prompt_tokens if usage else 0,
            output_tokens=usage.completion_tokens if usage else 0,
        )


class AzureOpenAILLM(LLM):
    """Azure OpenAI chat over a client from rag.core.llm.azure (v1 or classic wire)."""

    def __init__(self, client: Any, model: str):
        self._client = client
        # The model name is the Azure deployment name.
        self.model = model
        self._extra: Dict[str, Any] = {} if _REASONING_MODEL_RX.search(model or "") else {"temperature": 0}

    def _complete(self, messages: List[Any]) -> LLMResult:
        resp = self._client.chat.completions.create(model=self.model, messages=messages, **self._extra)
        # Azure can return no choices, e.g. when its content filter withholds the answer.
        text = (resp.choices[0].message.content or "") if resp.choices else ""
        usage = resp.usage
        return LLMResult(
            text=text,
            input_tokens=usage.prompt_tokens if usage else 0,
            output_tokens=usage.completion_tokens if usage else 0,
        )

    def generate(self, prompt: str) -> LLMResult:
        return self._complete([{"role": "user", "content": _ensure_user_content(prompt)}])

    def generate_with_images(self, prompt: str, images: List[Dict[str, str]]) -> LLMResult:
        return self._complete([_user_message(_openai_image_content(prompt, images))])


class AnthropicLLM(LLM):
    """Anthropic LLM using the anthropic SDK directly."""

    def __init__(self, model: str, api_key: str):
        import anthropic

        self._client = anthropic.Anthropic(api_key=api_key)
        self.model = model

    def generate(self, prompt: str) -> LLMResult:
        resp = self._client.messages.create(
            model=self.model,
            max_tokens=1024,
            temperature=0,
            messages=[{"role": "user", "content": _ensure_user_content(prompt)}],
        )
        first_block = resp.content[0] if resp.content else None
        text = first_block.text if first_block and hasattr(first_block, "text") else ""
        return LLMResult(
            text=text,
            input_tokens=resp.usage.input_tokens,
            output_tokens=resp.usage.output_tokens,
        )

    def generate_with_images(self, prompt: str, images: List[Dict[str, str]]) -> LLMResult:
        content: List[Dict[str, Any]] = [
            {
                "type": "image",
                "source": {"type": "base64", "media_type": img["mime_type"], "data": img["data"]},
            }
            for img in images
        ]
        content.append({"type": "text", "text": _ensure_user_content(prompt)})
        resp = self._client.messages.create(
            model=self.model,
            max_tokens=1024,
            temperature=0,
            messages=[_user_message(content)],
        )
        first_block = resp.content[0] if resp.content else None
        text = first_block.text if first_block and hasattr(first_block, "text") else ""
        return LLMResult(
            text=text,
            input_tokens=resp.usage.input_tokens,
            output_tokens=resp.usage.output_tokens,
        )


class BedrockLLM(LLM):
    """AWS Bedrock LLM using boto3."""

    def __init__(self, client: Any, model: str):
        self._client = client
        self.model = model

    def generate(self, prompt: str) -> LLMResult:
        body = json.dumps(
            {
                "prompt": prompt,
                "temperature": 0.3,
                "top_p": 0.9,
                "max_gen_len": 1024,
            }
        )
        resp = self._client.invoke_model(modelId=self.model, body=body, contentType="application/json")
        result = json.loads(resp["body"].read())
        text = result.get("generation", result.get("completions", [{}])[0].get("data", {}).get("text", str(result)))
        input_tokens = result.get("prompt_token_count", 0)
        output_tokens = result.get("generation_token_count", 0)
        return LLMResult(text=text, input_tokens=input_tokens, output_tokens=output_tokens)

    def generate_with_images(self, prompt: str, images: List[Dict[str, str]]) -> LLMResult:
        """Describe images via the Converse API.

        ``generate`` above speaks the llama-style invoke_model body, which has no
        way to carry an image. Converse is the only Bedrock surface that does,
        and a model that cannot see rejects the call with a ValidationException —
        which is what the caller treats as "this model has no vision".
        """
        content: List[Dict[str, Any]] = [
            {
                "image": {
                    "format": _bedrock_image_format(img["mime_type"]),
                    "source": {"bytes": base64.b64decode(img["data"])},
                }
            }
            for img in images
        ]
        content.append({"text": _ensure_user_content(prompt)})
        resp = self._client.converse(
            modelId=self.model,
            messages=[{"role": "user", "content": content}],
            inferenceConfig={"temperature": 0, "maxTokens": 1024},
        )
        blocks = resp.get("output", {}).get("message", {}).get("content", [])
        text = next((b["text"] for b in blocks if "text" in b), "")
        usage = resp.get("usage", {})
        return LLMResult(
            text=text,
            input_tokens=int(usage.get("inputTokens", 0)),
            output_tokens=int(usage.get("outputTokens", 0)),
        )


class GoogleGenAILLM(LLM):
    """Google Generative AI LLM using google-genai SDK."""

    def __init__(self, model: str, api_key: str):
        from google import genai

        self._client = genai.Client(api_key=api_key)
        self.model = model

    def generate(self, prompt: str) -> LLMResult:
        resp = self._client.models.generate_content(
            model=self.model,
            contents=prompt,
            config={"temperature": 0},
        )
        text = resp.text or ""
        usage = resp.usage_metadata
        return LLMResult(
            text=text,
            input_tokens=int(usage.prompt_token_count or 0) if usage else 0,
            output_tokens=int(usage.candidates_token_count or 0) if usage else 0,
        )

    def generate_with_images(self, prompt: str, images: List[Dict[str, str]]) -> LLMResult:
        from google.genai import types

        parts: List[Any] = [
            types.Part.from_bytes(data=base64.b64decode(img["data"]), mime_type=img["mime_type"]) for img in images
        ]
        parts.append(_ensure_user_content(prompt))
        resp = self._client.models.generate_content(
            model=self.model,
            contents=parts,
            config={"temperature": 0},
        )
        text = resp.text or ""
        usage = resp.usage_metadata
        return LLMResult(
            text=text,
            input_tokens=int(usage.prompt_token_count or 0) if usage else 0,
            output_tokens=int(usage.candidates_token_count or 0) if usage else 0,
        )


class HuggingFaceLLM(LLM):
    """HuggingFace Inference API LLM using requests."""

    def __init__(self, model: str, endpoint_url: str, api_key: str):
        self._endpoint_url = endpoint_url
        self._api_key = api_key
        self.model = model

    def generate(self, prompt: str) -> LLMResult:
        import requests

        headers = {"Authorization": f"Bearer {self._api_key}"}
        payload = {"inputs": prompt, "parameters": {"temperature": 0.01, "max_new_tokens": 1024}}
        resp = requests.post(self._endpoint_url, headers=headers, json=payload, timeout=120)
        resp.raise_for_status()
        result = resp.json()
        text = result[0]["generated_text"] if isinstance(result, list) else str(result)
        return LLMResult(text=text)


class SageMakerLLM(LLM):
    """AWS SageMaker endpoint LLM using boto3."""

    def __init__(self, endpoint_name: str, region_name: str):
        import boto3

        self._client = boto3.client("sagemaker-runtime", region_name=region_name)
        self._endpoint_name = endpoint_name
        self.model = endpoint_name

    def generate(self, prompt: str) -> LLMResult:
        body = json.dumps({"prompt": prompt})
        resp = self._client.invoke_endpoint(
            EndpointName=self._endpoint_name,
            ContentType="application/json",
            Body=body.encode("utf-8"),
        )
        result = json.loads(resp["Body"].read().decode("utf-8"))
        text = result[0]["generated_text"] if isinstance(result, list) else str(result)
        return LLMResult(text=text)
