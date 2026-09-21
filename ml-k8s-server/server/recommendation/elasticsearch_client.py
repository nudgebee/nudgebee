"""Shared Elasticsearch transport for the recommendation services.

Both vertical (pod) and volume (PVC) rightsizing query the same Elastic Agent cluster
with the same credentials, which travel with the trigger request. The auth handling is
here rather than in each service so the two cannot drift — in particular the cognito
refusal, which is a correctness guard and not a nicety: SigV4 signing exists only on the
Go side, so an unsigned request returns 403, and a caller that treats 403 as "no data"
reports a cluster with metrics as having none.
"""

from __future__ import annotations

import asyncio
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Dict, Optional, Tuple

import requests

DEFAULT_INDEX = "metrics-*"
REQUEST_TIMEOUT_SECONDS = 60


class ElasticsearchTransport:
    """Posts _search bodies to one Elasticsearch, with the request's credentials."""

    def __init__(
        self,
        url: str,
        auth_type: Optional[str] = None,
        username: Optional[str] = None,
        password: Optional[str] = None,
        api_key: Optional[str] = None,
        bearer_token: Optional[str] = None,
        metrics_index: Optional[str] = None,
        tls_skip_verify: bool = False,
    ) -> None:
        if not url:
            raise ValueError("Elasticsearch requires a url")
        self._url = url.rstrip("/")
        self._index = metrics_index or DEFAULT_INDEX
        self._verify = not tls_skip_verify
        self._auth: Optional[Tuple[str, str]] = None
        self._headers: Dict[str, str] = {"Content-Type": "application/json"}

        auth_type = (auth_type or "basic").lower()
        if auth_type == "cognito":
            raise ValueError("Elasticsearch recommendations do not support cognito authentication")
        if auth_type == "api_key":
            self._headers["Authorization"] = f"ApiKey {api_key}"
        elif auth_type == "bearer_token":
            self._headers["Authorization"] = f"Bearer {bearer_token}"
        else:
            self._auth = (username or "", password or "")

    @classmethod
    def from_config(cls, config: Optional[dict]) -> "ElasticsearchTransport":
        """Build from the `elasticsearch` block carried on the trigger request."""
        cfg = config or {}
        return cls(
            url=cfg.get("url", ""),
            auth_type=cfg.get("auth_type"),
            username=cfg.get("username"),
            password=cfg.get("password"),
            api_key=cfg.get("api_key"),
            bearer_token=cfg.get("bearer_token"),
            metrics_index=cfg.get("metrics_index"),
            tls_skip_verify=bool(cfg.get("tls_skip_verify", False)),
        )

    @property
    def index(self) -> str:
        return self._index

    def check_connection(self) -> None:
        """Fail loudly if the cluster is unreachable or rejects our credentials.

        Callers treat an exception here as "this backend is unusable", which is the
        point: swallowing it would be read as "this cluster has no metrics".
        """
        try:
            resp = requests.get(
                f"{self._url}/_cluster/health",
                headers=self._headers,
                auth=self._auth,
                verify=self._verify,
                timeout=10,
            )
            resp.raise_for_status()
        except Exception as e:
            raise ConnectionError(f"Elasticsearch connection failed: {e}") from e

    def search(self, body: Dict[str, Any]) -> Dict[str, Any]:
        resp = requests.post(
            f"{self._url}/{self._index}/_search",
            json=body,
            headers=self._headers,
            auth=self._auth,
            verify=self._verify,
            timeout=REQUEST_TIMEOUT_SECONDS,
        )
        resp.raise_for_status()
        payload: Dict[str, Any] = resp.json()
        return payload

    async def async_search(self, body: Dict[str, Any], executor: ThreadPoolExecutor) -> Dict[str, Any]:
        loop = asyncio.get_running_loop()
        return await loop.run_in_executor(executor, lambda: self.search(body))
