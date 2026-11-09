"""OpenAI judge factory wiring and local SDK request tests."""

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread
from unittest.mock import Mock

import pytest

from benchmark_server.common.llm import get_openai_llm
from benchmark_server.utils.utils import Config


@pytest.mark.parametrize(
    "endpoint,api_type,expected_url",
    [
        ("https://judge.example/v1", "openai", "https://judge.example/v1"),
        ("https://judge.example/v1", "compatible", "https://judge.example/v1"),
        ("https://judge.example/v1", "", "https://judge.example/v1"),
        ("", "openai", None),
        ("", "", None),
    ],
)
def test_openai_judge_factory_endpoint(endpoint, api_type, expected_url, monkeypatch):
    monkeypatch.setattr(Config, "eval_llm_provider_api_endpoint", endpoint)
    monkeypatch.setattr(Config, "eval_llm_provider_api_type", api_type)
    monkeypatch.setattr(Config, "eval_llm_provider_api_key", "judge-test-key")
    client = object()
    constructor = Mock(return_value=client)
    monkeypatch.setattr("benchmark_server.common.llm.ChatOpenAI", constructor)

    assert get_openai_llm("judge-model") is client
    constructor.assert_called_once()
    kwargs = constructor.call_args.kwargs
    assert kwargs["base_url"] == expected_url
    assert kwargs["model"] == "judge-model"
    assert kwargs["api_key"].get_secret_value() == "judge-test-key"
    assert kwargs["temperature"] == 0
    assert kwargs["streaming"] is True


def test_openai_judge_factory_uses_configured_endpoint_over_http(monkeypatch):
    requests = []

    class OpenAIHandler(BaseHTTPRequestHandler):
        def do_POST(self):
            body = self.rfile.read(int(self.headers["Content-Length"]))
            requests.append((self.path, json.loads(body)))
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            stop_choice = {"index": 0, "delta": {}, "finish_reason": "stop"}
            chunks = [
                {
                    "id": "chatcmpl-local",
                    "object": "chat.completion.chunk",
                    "created": 0,
                    "model": "judge-model",
                    "choices": [
                        {
                            "index": 0,
                            "delta": {
                                "role": "assistant",
                                "content": "served locally",
                            },
                            "finish_reason": None,
                        }
                    ],
                },
                {
                    "id": "chatcmpl-local",
                    "object": "chat.completion.chunk",
                    "created": 0,
                    "model": "judge-model",
                    "choices": [stop_choice],
                },
            ]
            for chunk in chunks:
                self.wfile.write(f"data: {json.dumps(chunk)}\n\n".encode())
            self.wfile.write(b"data: [DONE]\n\n")

        def log_message(self, format, *args):
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), OpenAIHandler)
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        monkeypatch.setattr(
            Config,
            "eval_llm_provider_api_endpoint",
            f"http://127.0.0.1:{server.server_port}/v1",
        )
        monkeypatch.setattr(Config, "eval_llm_provider_api_type", "openai")
        monkeypatch.setattr(Config, "eval_llm_provider_api_key", "judge-test-key")
        llm = get_openai_llm("judge-model")
        assert str(llm.root_client.base_url).rstrip("/") == (
            f"http://127.0.0.1:{server.server_port}/v1"
        )
        result = llm.invoke("local smoke")
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)

    assert result.content == "served locally"
    assert len(requests) == 1
    assert requests[0][0] == "/v1/chat/completions"
    assert requests[0][1]["model"] == "judge-model"
    assert requests[0][1]["messages"][-1]["content"] == "local smoke"
