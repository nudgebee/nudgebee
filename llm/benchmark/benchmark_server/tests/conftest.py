"""Shared test fixtures for benchmark_server.

The auth modules load ``NEXTAUTH_PRIVATE_KEY`` at import time, so this
conftest generates an ephemeral RSA keypair and exports it via env vars
**before** any test imports those modules. That way the modules pick up
our test key naturally — no monkey-patching of module globals needed.

``APP_DATABASE_URL`` is also set to a stub so ``db_utils.db_engine``
fails to connect (returns ``None``); the route-level tests rely on
that to short-circuit DB queries instead of needing a real database.
"""

import os
from datetime import datetime, timedelta, timezone

import jwt as pyjwt
import pytest
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa

# --- ephemeral RSA keypair (one per test session) ----------------------
_KEY = rsa.generate_private_key(public_exponent=65537, key_size=2048)
PRIVATE_PEM = _KEY.private_bytes(
    encoding=serialization.Encoding.PEM,
    format=serialization.PrivateFormat.PKCS8,
    encryption_algorithm=serialization.NoEncryption(),
).decode("ascii")

# Inject BEFORE importing any benchmark_server.* module that reads these.
os.environ["NEXTAUTH_PRIVATE_KEY"] = PRIVATE_PEM
os.environ.setdefault("APP_DATABASE_URL", "postgresql://test:test@localhost:1/test")
os.environ.setdefault("BENCHMARK_INSECURE_COOKIES", "1")  # tests aren't HTTPS


# --- JWT factories -----------------------------------------------------
@pytest.fixture
def session_jwt_factory():
    """Mint a benchmark-server session JWT with overridable claims.

    Defaults to a valid token; pass kwargs to flip individual claims
    (``iss=``, ``aud=``, ``exp=``, ``user_id=``) for negative tests.
    """

    def _mint(**overrides):
        # Lazy import so env vars are set before module load
        from benchmark_server.controllers.auth_controller import (
            JWT_AUDIENCE,
            JWT_ISSUER,
            SESSION_MAX_AGE,
        )

        now = datetime.now(timezone.utc)
        payload = {
            "user_id": "user-123",
            "email": "test@example.com",
            "display_name": "Test User",
            "iat": now,
            "exp": now + timedelta(seconds=SESSION_MAX_AGE),
            "iss": JWT_ISSUER,
            "aud": JWT_AUDIENCE,
        }
        payload.update(overrides)
        return pyjwt.encode(payload, PRIVATE_PEM, algorithm="RS256")

    return _mint


# --- Authz / AuthUser helpers -----------------------------------------
@pytest.fixture
def make_authz():
    """Build an ``Authz`` with given is_super_admin and tenant memberships."""

    def _make(is_super_admin=False, tenants=None):
        from benchmark_server.middleware.authz import Authz, TenantAccess

        ta_objs = []
        for t in tenants or []:
            if isinstance(t, str):
                ta_objs.append(TenantAccess(tenant_id=t, role="user"))
            else:
                ta_objs.append(
                    TenantAccess(
                        tenant_id=t["id"],
                        role=t.get("role", "user"),
                        account_ids=t.get("account_ids", []),
                    )
                )
        return Authz(user_id="user-123", is_super_admin=is_super_admin, tenants=ta_objs)

    return _make


@pytest.fixture
def make_user():
    def _make(user_id="user-123", email="test@example.com"):
        from benchmark_server.middleware.auth import AuthUser

        return AuthUser(user_id=user_id, email=email, display_name=email)

    return _make
