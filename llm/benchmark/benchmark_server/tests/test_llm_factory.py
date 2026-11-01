"""Isolated factory wiring tests; provider SDK imports and HTTP are not exercised."""

import ast
import logging
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from pydantic import SecretStr


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
def test_openai_judge_factory_endpoint(endpoint, api_type, expected_url):
    # Compile the actual factory without importing every unrelated provider SDK.
    source_path = Path(__file__).parents[1] / "common" / "llm.py"
    source = ast.parse(source_path.read_text(encoding="utf-8"))
    factory_node = next(
        node
        for node in source.body
        if isinstance(node, ast.FunctionDef) and node.name == "get_openai_llm"
    )
    client = object()
    constructor = Mock(return_value=client)
    namespace = {
        "Config": SimpleNamespace(
            eval_llm_provider_api_endpoint=endpoint,
            eval_llm_provider_api_type=api_type,
            eval_llm_provider_api_key="judge-test-key",
        ),
        "ChatOpenAI": constructor,
        "SecretStr": SecretStr,
        "logger": logging.getLogger(__name__),
    }
    exec(
        compile(
            ast.Module(body=[factory_node], type_ignores=[]), str(source_path), "exec"
        ),
        namespace,
    )

    assert namespace["get_openai_llm"]("judge-model") is client
    constructor.assert_called_once()
    kwargs = constructor.call_args.kwargs
    assert kwargs["base_url"] == expected_url
    assert kwargs["model"] == "judge-model"
    assert kwargs["api_key"].get_secret_value() == "judge-test-key"
    assert kwargs["temperature"] == 0
    assert kwargs["streaming"] is True
