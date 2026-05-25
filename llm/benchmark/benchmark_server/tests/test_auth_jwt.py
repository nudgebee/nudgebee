"""Session JWT validation CVE-class matrix tests.

Covers the threat model where ``NEXTAUTH_PRIVATE_KEY`` is shared across
NextAuth, api-server, and any other Nudgebee service. Without strict
``iss`` / ``aud`` validation, an RS256 JWT minted by any of those
services would be accepted as a benchmark session — these tests pin
the validation to ``benchmark-server`` / ``benchmark``.
"""

from datetime import datetime, timedelta, timezone

import jwt as pyjwt
import pytest


def test_session_valid_token_extracts_user(session_jwt_factory):
    from benchmark_server.middleware.auth import _extract_user_from_session

    user = _extract_user_from_session(session_jwt_factory())
    assert user.user_id == "user-123"
    assert user.email == "test@example.com"


def test_session_wrong_iss_rejected(session_jwt_factory):
    """A token signed with the right key but wrong issuer must be rejected.
    Cross-service token confusion: ``api-server`` could mint a JWT with
    its own issuer that is signed by the same NEXTAUTH_PRIVATE_KEY."""
    from benchmark_server.middleware.auth import _extract_user_from_session

    token = session_jwt_factory(iss="api-server")
    with pytest.raises(pyjwt.InvalidIssuerError):
        _extract_user_from_session(token)


def test_session_wrong_aud_rejected(session_jwt_factory):
    """Token confusion via mismatched audience."""
    from benchmark_server.middleware.auth import _extract_user_from_session

    token = session_jwt_factory(aud="other-service")
    with pytest.raises(pyjwt.InvalidAudienceError):
        _extract_user_from_session(token)


def test_session_expired_rejected(session_jwt_factory):
    from benchmark_server.middleware.auth import _extract_user_from_session

    token = session_jwt_factory(
        iat=datetime.now(timezone.utc) - timedelta(hours=2),
        exp=datetime.now(timezone.utc) - timedelta(hours=1),
    )
    with pytest.raises(pyjwt.ExpiredSignatureError):
        _extract_user_from_session(token)


def test_session_tampered_rejected(session_jwt_factory):
    """Signature must verify — flipping a payload byte invalidates it."""
    from benchmark_server.middleware.auth import _extract_user_from_session

    token = session_jwt_factory()
    parts = token.split(".")
    tampered = parts[0] + "." + parts[1][:-1] + "X" + "." + parts[2]
    with pytest.raises(pyjwt.InvalidSignatureError):
        _extract_user_from_session(tampered)


def test_session_missing_user_id_rejected(session_jwt_factory):
    """A token signed correctly but with empty user_id is unusable."""
    from benchmark_server.middleware.auth import _extract_user_from_session

    token = session_jwt_factory(user_id="")
    with pytest.raises(ValueError, match="user_id"):
        _extract_user_from_session(token)
