"""The LLM trigger's call to llm-server carries the service token.

llm-server refuses requests without a matching X-ACTION-TOKEN once its
LLM_SERVER_TOKEN is set, which the Helm chart now always does.
"""

from config import Configs
from handlers.event_handler import LLMTriggerHandler


def test_sends_the_service_token_when_configured(monkeypatch):
    monkeypatch.setattr(Configs, "LLM_SERVER_TOKEN", "svc-token")
    headers = LLMTriggerHandler._build_llm_headers(None, "tenant-1", "user-1")
    assert headers["X-ACTION-TOKEN"] == "svc-token"
    assert headers["x-tenant-id"] == "tenant-1"
    assert headers["x-user-id"] == "user-1"


def test_omits_the_header_when_no_token_is_configured(monkeypatch):
    monkeypatch.setattr(Configs, "LLM_SERVER_TOKEN", "")
    headers = LLMTriggerHandler._build_llm_headers(None, "tenant-1", "user-1")
    assert "X-ACTION-TOKEN" not in headers
