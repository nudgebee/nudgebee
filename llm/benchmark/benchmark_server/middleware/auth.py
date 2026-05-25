"""Authentication middleware for the benchmark server.

The dashboard authenticates via a **session cookie** set by the
magic-link callback at ``/api/auth/callback/email``. The cookie carries
an RS256-signed JWT with user identity, validated here on every request.

Unauthenticated browser requests are redirected to ``/signin``.
Unauthenticated API requests receive a ``401`` JSON response.
"""

import logging
import os
from dataclasses import dataclass
from typing import Optional

import jwt
from cryptography.hazmat.primitives.serialization import load_pem_public_key
from fastapi import HTTPException, Request
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.responses import JSONResponse, RedirectResponse

from benchmark_server.utils.jwt_keys import load_private_key_tolerant

logger = logging.getLogger(__name__)

SESSION_COOKIE = "benchmark_session"

# Pinned ``iss`` / ``aud`` for benchmark-minted session tokens. Without
# this, any RS256 JWT signed with the shared NEXTAUTH_PRIVATE_KEY (and
# carrying a top-level ``user_id`` claim) would be accepted here —
# including tokens minted by NextAuth, the api-server, or any other
# Nudgebee service that uses the same key. Pinning these closes the
# cross-service token-confusion gap. Mirrored in
# ``controllers/auth_controller.py`` (the mint side).
SESSION_JWT_ISSUER = "benchmark-server"
SESSION_JWT_AUDIENCE = "benchmark"

# --- Key loading ---
_JWT_KEY_RAW = os.environ.get("NEXTAUTH_PRIVATE_KEY", "").replace("\\n", "\n").strip()

_jwt_public_key = None
if _JWT_KEY_RAW:
    try:
        # Tolerant loader handles the cluster's non-canonical-d key (see
        # ``utils/jwt_keys.py``) — for a normal key this is identical to
        # ``load_pem_private_key``; only the fallback path differs.
        _private = load_private_key_tolerant(_JWT_KEY_RAW.encode())
        _jwt_public_key = _private.public_key()
        logger.info("Loaded JWT verification key from NEXTAUTH_PRIVATE_KEY (RSA)")
    except Exception:
        try:
            _jwt_public_key = load_pem_public_key(_JWT_KEY_RAW.encode())
            logger.info("Loaded JWT verification key as public key")
        except Exception as e:
            logger.error("Failed to load JWT key: %s", e)

# Routes that skip authentication
PUBLIC_PATHS = frozenset({"/health", "/docs", "/openapi.json", "/redoc", "/signin"})
# ``/auth/`` and ``/api/auth/`` cover the magic-link surface (form post,
# email-callback). ``/signin`` is the HTML login page itself.
PUBLIC_PREFIXES = ("/static/", "/auth/", "/api/auth/")

# Methods that mutate state — Origin/Referer must match an allowed origin
# for these to defend against cross-site forgery. Cookie's samesite=strict
# already blocks browser cross-site cookie attachment, but the Origin
# check is defence-in-depth and protects against the rare browser bug or
# misconfiguration.
_CSRF_PROTECTED_METHODS = frozenset({"POST", "PUT", "PATCH", "DELETE"})


def _is_origin_allowed(request: "Request") -> bool:
    """Return True iff the request's Origin (or Referer) matches the host.

    Same-origin requests pass. Requests without an Origin/Referer header
    (curl, server-to-server) also pass — they aren't browser-driven, so
    they're not at CSRF risk, and the caller only consults this check
    when a session cookie is attached anyway.
    """
    origin = request.headers.get("origin", "")
    referer = request.headers.get("referer", "")
    if not origin and not referer:
        # No Origin: not browser-driven (curl, server-to-server). Allowed.
        return True
    host = request.headers.get("host", "")
    if not host:
        return False
    expected_https = f"https://{host}"
    expected_http = f"http://{host}"

    def _matches(url: str) -> bool:
        return (
            url == expected_https
            or url == expected_http
            or url.startswith(expected_https + "/")
            or url.startswith(expected_http + "/")
        )

    if origin and _matches(origin):
        return True
    if referer and _matches(referer):
        return True
    return False


@dataclass
class AuthUser:
    """Authenticated user — identity only.

    Authorization (which tenants / accounts / role) is fetched separately
    via ``benchmark_server.middleware.authz.get_user_authz(user.user_id)``
    so it can be sourced from the api-server schema (the source of truth)
    rather than duplicated in the JWT (which would go stale and bloat
    the cookie).
    """

    user_id: str
    email: Optional[str] = None
    display_name: Optional[str] = None


def _extract_user_from_session(token: str) -> AuthUser:
    """Decode a session JWT created by ``/auth/callback``. Identity-only.

    The JWT carries just enough to identify the user — ``user_id`` plus a
    couple of display fields. Authorization is fetched DB-side so RBAC
    changes don't require the user to re-log in.
    """
    if not _jwt_public_key:
        raise ValueError("JWT key not configured")

    payload = jwt.decode(
        token,
        _jwt_public_key,
        algorithms=["RS256"],
        audience=SESSION_JWT_AUDIENCE,
        issuer=SESSION_JWT_ISSUER,
        leeway=10,
    )
    user_id = payload.get("user_id", "")
    if not user_id:
        raise ValueError("Session token missing user_id")

    return AuthUser(
        user_id=user_id,
        email=payload.get("email"),
        display_name=payload.get("display_name"),
    )


class AuthMiddleware(BaseHTTPMiddleware):
    """Validate the session cookie on every request."""

    async def dispatch(self, request: Request, call_next):
        path = request.url.path
        is_public = path in PUBLIC_PATHS or any(
            path.startswith(p) for p in PUBLIC_PREFIXES
        )

        # Always attempt to populate request.state.user from the session
        # cookie — regardless of whether the path is public. This lets
        # public endpoints like /auth/me read the authenticated user (when
        # one exists) without requiring auth themselves. The "is_public"
        # branch below decides whether to ENFORCE auth (redirect / 401) for
        # non-public paths; identity decoding happens unconditionally.
        session_cookie = request.cookies.get(SESSION_COOKIE)
        if session_cookie:
            try:
                request.state.user = _extract_user_from_session(session_cookie)
            except Exception as e:
                # Surface the decode failure once at warning level — silent
                # failure here is the difference between "the user signed in
                # successfully" and "the dashboard can't see them" and is
                # extremely hard to diagnose without a log line.
                logger.warning(
                    "auth: session cookie present but failed to decode (%s) "
                    "for path=%s — user will be treated as anonymous",
                    e,
                    path,
                )

        # Log authenticated request — invaluable for diagnosing why the
        # dashboard sees no data. Authz is not fetched here (would add a
        # DB query to every request even for static assets); handlers
        # fetch on demand via ``get_authz``, which hits the in-memory cache.
        _user = getattr(request.state, "user", None)
        if _user and not path.startswith("/static/"):
            logger.info(
                "auth: %s %s — user=%s",
                request.method,
                path,
                _user.email or _user.user_id,
            )

        if is_public:
            return await call_next(request)

        # CSRF defence-in-depth: state-changing browser requests must come
        # from a same-origin page. samesite=strict on the cookie already
        # blocks browser cross-site attachment; the Origin check is
        # defence-in-depth against the rare browser bug or misconfiguration.
        if request.method in _CSRF_PROTECTED_METHODS:
            session_cookie_present = bool(session_cookie)
            if session_cookie_present and not _is_origin_allowed(request):
                return JSONResponse(
                    status_code=403,
                    content={"detail": "Cross-origin request rejected (CSRF)."},
                )

        if getattr(request.state, "user", None):
            return await call_next(request)

        # Not authenticated — redirect browsers, 401 for API clients.
        accept = request.headers.get("accept", "")
        if "text/html" in accept:
            return RedirectResponse("/signin")

        return JSONResponse(
            status_code=401,
            content={"detail": "Authentication required. Sign in at /signin."},
        )


def get_current_user(request: Request) -> AuthUser:
    """Extract the authenticated user from request state.

    Use as a FastAPI dependency: ``user = Depends(get_current_user)``
    """
    user = getattr(request.state, "user", None)
    if not user:
        raise HTTPException(status_code=401, detail="Not authenticated")
    return user


def get_authz(request: Request):
    """FastAPI dependency that returns the user's authorization context.

    Memoised on ``request.state`` so multiple ``Depends(get_authz)`` calls
    in the same request share one Authz instance. Backed by the
    in-memory cache in ``benchmark_server.middleware.authz`` — typical
    cost is a dict lookup; only the first request after login (or after
    a cache miss / forced refresh) hits the DB.
    """
    user = get_current_user(request)
    cached = getattr(request.state, "authz", None)
    if cached is not None:
        return cached

    from benchmark_server.middleware.authz import get_user_authz

    authz = get_user_authz(user.user_id)
    request.state.authz = authz
    return authz
