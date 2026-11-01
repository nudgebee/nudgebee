"""Wire-level tests for the Azure OpenAI provider (v1 and classic, key and Entra ID).

A local HTTP server stands in for Azure and records what the client sends.
"""

import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

import pytest

from rag.core.embeddings.generator import get_azure_llm
from rag.core.embeddings.providers import AzureOpenAIEmbeddings
from rag.core.llm.azure import normalize_endpoint

_CHAT_REPLY = {
    "id": "x",
    "object": "chat.completion",
    "created": 1,
    "model": "m",
    "choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
    "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
}
_EMBED_REPLY = {"object": "list", "data": [{"object": "embedding", "index": 0, "embedding": [0.1, 0.2]}], "model": "m"}
_TOKEN_REPLY = {"access_token": "entra-token", "token_type": "Bearer", "expires_in": 3600}


@pytest.fixture
def fake_azure():
    seen = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            body = self.rfile.read(int(self.headers.get("Content-Length", 0))).decode()
            url = urlparse(self.path)
            seen.append(
                {
                    "path": url.path,
                    "query": parse_qs(url.query),
                    "api_key": self.headers.get("api-key"),
                    "authorization": self.headers.get("Authorization"),
                    "project": self.headers.get("X-Project"),
                    "body": body,
                }
            )
            if url.path == "/token":
                reply = _TOKEN_REPLY
            elif url.path.endswith("/embeddings"):
                reply = _EMBED_REPLY
            else:
                reply = _CHAT_REPLY
            payload = json.dumps(reply).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, *args):
            pass

    server = HTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield f"http://127.0.0.1:{server.server_port}", seen
    server.shutdown()


def _chat_calls(seen):
    return [s for s in seen if s["path"] != "/token"]


@pytest.mark.parametrize(
    "style,version,want_path",
    [
        ("", "", "/openai/v1/chat/completions"),
        ("v1", "2024-10-21", "/openai/v1/chat/completions"),
        ("classic", "2024-10-21", "/openai/deployments/gpt-5-mini/chat/completions"),
    ],
)
def test_azure_llm_wire_with_api_key(fake_azure, style, version, want_path):
    base, seen = fake_azure
    llm = get_azure_llm(
        "gpt-5-mini",
        {
            "llm_provider_api_endpoint": base + "/openai/v1/",
            "llm_provider_api_style": style,
            "llm_provider_api_version": version,
            "llm_provider_api_key": "secret-key",
        },
    )
    assert llm.generate("hi").text == "ok"
    call = _chat_calls(seen)[-1]
    assert call["path"] == want_path
    body = json.loads(call["body"])
    assert "temperature" not in body, "gpt-5 models reject a non-default temperature"
    if style == "classic":
        assert call["query"] == {"api-version": ["2024-10-21"]}
        assert call["api_key"] == "secret-key"
    else:
        assert call["query"] == {}
        assert call["authorization"] == "Bearer secret-key"
        assert body["model"] == "gpt-5-mini"


@pytest.mark.parametrize("style", ["v1", "classic"])
def test_azure_llm_entra_token(fake_azure, style):
    base, seen = fake_azure
    llm = get_azure_llm(
        "gpt-5-mini",
        {
            "llm_provider_api_endpoint": base,
            "llm_provider_api_style": style,
            "llm_provider_api_version": "2024-10-21",
            "llm_auth_type": "oauth_client_credentials",
            "llm_oauth_token_url": base + "/token",
            "llm_oauth_client_id": "client",
            "llm_oauth_client_secret": "secret",
            "llm_oauth_scope": "https://cognitiveservices.azure.com/.default",
        },
    )
    llm.generate("hi")
    llm.generate("again")
    token_calls = [s for s in seen if s["path"] == "/token"]
    assert len(token_calls) == 1, "the token is cached across calls"
    form = parse_qs(token_calls[0]["body"])
    assert form["grant_type"] == ["client_credentials"]
    assert form["scope"] == ["https://cognitiveservices.azure.com/.default"]
    for call in _chat_calls(seen):
        assert call["authorization"] == "Bearer entra-token"
        assert call["api_key"] is None, "the placeholder key must never reach Azure"


def test_azure_llm_keeps_temperature_for_non_reasoning_models(fake_azure):
    base, seen = fake_azure
    get_azure_llm(
        "gpt-4o", {"llm_provider_api_endpoint": base, "llm_provider_api_key": "k", "llm_provider_api_style": "v1"}
    ).generate("hi")
    assert json.loads(_chat_calls(seen)[-1]["body"])["temperature"] == 0


@pytest.mark.parametrize(
    "cfg,message",
    [
        ({"llm_provider_api_key": "k"}, "endpoint"),
        ({"llm_provider_api_endpoint": "https://x.openai.azure.com"}, "API key or Entra ID"),
        (
            {
                "llm_provider_api_endpoint": "https://x.openai.azure.com",
                "llm_provider_api_key": "k",
                "llm_provider_api_style": "classic",
            },
            "requires an API version",
        ),
        (
            {
                "llm_provider_api_endpoint": "https://x.openai.azure.com",
                "llm_provider_api_key": "k",
                "llm_provider_api_style": "v2",
            },
            "must be v1 or classic",
        ),
        (
            {"llm_provider_api_endpoint": "https://x.openai.azure.com", "llm_auth_type": "oauth_client_credentials"},
            "llm_oauth_token_url",
        ),
    ],
)
def test_azure_llm_config_errors(cfg, message):
    full = {
        "llm_provider_api_endpoint": "",
        "llm_provider_api_key": "",
        "llm_provider_api_style": "",
        "llm_provider_api_version": "",
        "llm_auth_type": "",
    }
    full.update(cfg)
    with pytest.raises(ValueError, match=message):
        get_azure_llm("gpt-5-mini", full)


@pytest.mark.parametrize(
    "style,want_path",
    [("v1", "/openai/v1/embeddings"), ("classic", "/openai/deployments/text-embedding-3-small/embeddings")],
)
def test_azure_embeddings_wire(fake_azure, style, want_path):
    base, seen = fake_azure
    emb = AzureOpenAIEmbeddings(
        api_key="k", model="text-embedding-3-small", api_version="2024-10-21", azure_endpoint=base, api_style=style
    )
    assert emb.embed_query("hello") == [0.1, 0.2]
    assert seen[-1]["path"] == want_path


def test_openai_provider_with_azure_type_rejects_a_missing_api_version():
    from rag.core.embeddings.generator import build_embeddings_provider_map

    cfg = {
        "embeddings_api_type": "azure",
        "embeddings_api_key": "k",
        "embeddings_model_id": "text-embedding-3-small",
        "embeddings_api_endpoint": "https://x.openai.azure.com",
    }
    provider_map, _ = build_embeddings_provider_map(lambda key, default=None: cfg.get(key, default), None)
    with pytest.raises(ValueError, match="requires an API version"):
        provider_map["openai"]()


@pytest.mark.parametrize("expires_in", [None, "3599", 3599])
def test_entra_token_tolerates_null_or_string_expiry(monkeypatch, expires_in):
    from types import SimpleNamespace

    from rag.core.llm import azure

    reply = {"access_token": "entra-token", "expires_in": expires_in}
    monkeypatch.setattr(azure.httpx, "post", lambda *a, **k: SimpleNamespace(status_code=200, json=lambda: reply))
    auth = azure.ClientCredentialsAuth("https://login.example/token", "client", "secret")
    assert auth.token() == "entra-token"


def test_entra_token_reuses_the_cached_token_when_an_early_refresh_fails(monkeypatch):
    from types import SimpleNamespace

    from rag.core.llm import azure

    replies = [SimpleNamespace(status_code=200, json=lambda: {"access_token": "first", "expires_in": 3600})]

    def post(*_a, **_k):
        if replies:
            return replies.pop()
        raise azure.httpx.ConnectError("token endpoint down")

    monkeypatch.setattr(azure.httpx, "post", post)
    auth = azure.ClientCredentialsAuth("https://login.example/token", "client", "secret")
    assert auth.token() == "first"

    auth._expires_at = azure.time.time() + 60  # inside the refresh margin, not yet expired
    assert auth.token() == "first"

    auth._expires_at = azure.time.time() - 1  # expired: the failure must surface
    with pytest.raises(azure.httpx.ConnectError):
        auth.token()


def test_normalize_endpoint():
    assert normalize_endpoint(" https://x.openai.azure.com/ ") == "https://x.openai.azure.com"
    assert normalize_endpoint("https://x.openai.azure.com/openai") == "https://x.openai.azure.com"
    assert normalize_endpoint("https://x.openai.azure.com/openai/v1/") == "https://x.openai.azure.com"


def test_azure_llm_handles_a_reply_with_no_choices():
    from types import SimpleNamespace

    from rag.core.llm.providers import AzureOpenAILLM

    empty = SimpleNamespace(choices=[], usage=None)
    client = SimpleNamespace(chat=SimpleNamespace(completions=SimpleNamespace(create=lambda **_: empty)))
    result = AzureOpenAILLM(client=client, model="gpt-5-mini").generate("hi")
    assert result.text == ""


def test_azure_llm_sends_gateway_headers_but_never_auth_overrides(fake_azure):
    base, seen = fake_azure
    get_azure_llm(
        "gpt-5-mini",
        {
            "llm_provider_api_endpoint": base,
            "llm_provider_api_key": "k",
            "llm_provider_api_style": "classic",
            "llm_provider_api_version": "2024-10-21",
            "llm_extra_headers": '{"X-Project": "abc-123", "api-key": "sneaky", "Authorization": "Bearer sneaky"}',
        },
    ).generate("hi")
    call = _chat_calls(seen)[-1]
    assert call["project"] == "abc-123"
    assert call["api_key"] == "k"
    assert call["authorization"] is None
