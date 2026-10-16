"""Azure OpenAI client construction shared by the chat and embeddings paths.

Mirrors llm-server's agents/core/llm_config.go (getAzureOpenAILLM). ``api_style`` picks the wire:

    v1       {endpoint}/openai/v1/...          deployment name in the request body (default)
    classic  {endpoint}/openai/deployments/{deployment}/...?api-version=X

Auth is the resource key, or an OAuth2 client-credentials token (Microsoft Entra ID)
read from the same llm_auth_type / llm_oauth_* keys llm-server uses.
"""

import json
import logging
import threading
import time
from typing import Any, Callable, Dict, Optional

import httpx

API_STYLE_V1 = "v1"
API_STYLE_CLASSIC = "classic"
OAUTH_AUTH_TYPE = "oauth_client_credentials"
_TOKEN_REFRESH_MARGIN_SECONDS = 300

logger = logging.getLogger(__name__)


def normalize_endpoint(endpoint: str) -> str:
    """Reduce a pasted endpoint to the resource root (drops a trailing /openai or /openai/v1)."""
    ep = (endpoint or "").strip().rstrip("/")
    for suffix in ("/openai/v1", "/openai"):
        if ep.lower().endswith(suffix):
            return ep[: -len(suffix)]
    return ep


class ClientCredentialsAuth:
    """Fetches an OAuth2 client-credentials token and caches it until shortly before expiry.

    Passed to the openai SDK as a token provider, so the SDK's own HTTP client sends it.
    """

    def __init__(self, token_url: str, client_id: str, client_secret: str, scope: str = ""):
        self._token_url = token_url
        self._client_id = client_id
        self._client_secret = client_secret
        self._scope = scope
        self._lock = threading.Lock()
        self._token: Optional[str] = None
        self._expires_at = 0.0

    def token(self) -> str:
        with self._lock:
            if self._token and time.time() < self._expires_at - _TOKEN_REFRESH_MARGIN_SECONDS:
                return self._token
            data = {
                "grant_type": "client_credentials",
                "client_id": self._client_id,
                "client_secret": self._client_secret,
            }
            if self._scope:
                data["scope"] = self._scope
            try:
                resp = httpx.post(self._token_url, data=data, timeout=30)
                if resp.status_code != 200:
                    raise RuntimeError(f"OAuth token request to {self._token_url} failed with HTTP {resp.status_code}")
                body = resp.json()
                self._token = body["access_token"]
                self._expires_at = time.time() + int(body.get("expires_in") or 3600)
            except Exception:
                # Refresh starts early, so the cached token is usually still valid.
                if self._token and time.time() < self._expires_at:
                    logger.warning(
                        "OAuth token refresh failed; reusing the cached token until it expires", exc_info=True
                    )
                    return self._token
                raise
            return self._token


def oauth_from_config(get: Callable[[str], str]) -> Optional[ClientCredentialsAuth]:
    """The token provider when llm_auth_type selects client credentials, else None."""
    if (get("llm_auth_type") or "").strip() != OAUTH_AUTH_TYPE:
        return None
    token_url = (get("llm_oauth_token_url") or "").strip()
    client_id = (get("llm_oauth_client_id") or "").strip()
    client_secret = (get("llm_oauth_client_secret") or "").strip()
    if not (token_url and client_id and client_secret):
        raise ValueError(
            "llm_oauth_token_url, llm_oauth_client_id and llm_oauth_client_secret are required "
            "when llm_auth_type is oauth_client_credentials"
        )
    return ClientCredentialsAuth(token_url, client_id, client_secret, (get("llm_oauth_scope") or "").strip())


def extra_headers_from_config(raw: str) -> Dict[str, str]:
    """Gateway headers from llm_extra_headers (a JSON object); auth headers are owned by the auth settings."""
    if not (raw or "").strip():
        return {}
    try:
        headers = json.loads(raw)
    except ValueError as exc:
        raise ValueError("llm_extra_headers must be a JSON object of string values") from exc
    if not isinstance(headers, dict):
        raise ValueError("llm_extra_headers must be a JSON object of string values")
    return {str(k): str(v) for k, v in headers.items() if str(k).lower() not in ("authorization", "api-key")}


def build_azure_openai_client(
    endpoint: str,
    api_style: str,
    api_version: str,
    api_key: str,
    oauth: Optional[ClientCredentialsAuth] = None,
    extra_headers: Optional[Dict[str, str]] = None,
) -> Any:
    """An openai SDK client for either Azure wire style. Callers pass the deployment name as ``model``."""
    import openai

    root = normalize_endpoint(endpoint)
    if not root:
        raise ValueError("azure provider requires an endpoint (e.g. https://<resource>.openai.azure.com)")
    style = (api_style or API_STYLE_V1).strip().lower()
    if style not in (API_STYLE_V1, API_STYLE_CLASSIC):
        raise ValueError(f"azure api style {api_style!r} is invalid; must be v1 or classic")
    key = (api_key or "").strip()
    if not key and not oauth:
        raise ValueError("azure provider requires an API key or Entra ID (oauth_client_credentials) settings")

    if style == API_STYLE_CLASSIC:
        version = (api_version or "").strip()
        if not version:
            raise ValueError("azure classic api style requires an API version")
        if oauth:
            return openai.AzureOpenAI(
                api_version=version,
                azure_endpoint=root,
                azure_ad_token_provider=oauth.token,
                default_headers=extra_headers,
            )
        return openai.AzureOpenAI(api_version=version, azure_endpoint=root, api_key=key, default_headers=extra_headers)
    # The openai SDK accepts a callable api_key and calls it per request, so the token refreshes.
    return openai.OpenAI(
        base_url=f"{root}/openai/v1", api_key=oauth.token if oauth else key, default_headers=extra_headers
    )
