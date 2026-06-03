"""Direct LLM providers replacing langchain wrappers.

Each provider implements the LLM ABC using native SDK calls.
Used exclusively for reranking — each provider sends a prompt and returns text + token usage.
"""

import json
import logging
from typing import Any, Dict, Optional

from rag.core.types import LLM, LLMResult

logger = logging.getLogger(__name__)


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
            messages=[{"role": "user", "content": prompt}],
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
    """Azure OpenAI LLM using the openai SDK with azure config."""

    def __init__(self, model: str, api_key: str, api_version: str, azure_endpoint: str):
        import openai

        self._client = openai.AzureOpenAI(
            api_key=api_key,
            api_version=api_version,
            azure_endpoint=azure_endpoint,
        )
        self.model = model

    def generate(self, prompt: str) -> LLMResult:
        resp = self._client.chat.completions.create(
            model=self.model,
            messages=[{"role": "user", "content": prompt}],
            temperature=0,
        )
        text = resp.choices[0].message.content or ""
        usage = resp.usage
        return LLMResult(
            text=text,
            input_tokens=usage.prompt_tokens if usage else 0,
            output_tokens=usage.completion_tokens if usage else 0,
        )


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
            messages=[{"role": "user", "content": prompt}],
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
