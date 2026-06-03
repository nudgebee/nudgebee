"""Magic-link email authentication for the benchmark dashboard.

The dashboard's browser flow is provider-agnostic: the user enters their
email, we send them a one-time link, they click it, we mint a session JWT.
Identity is delegated to whatever protects the user's inbox — Google SSO,
Okta, Azure AD, corporate Outlook with MFA, etc. — without coupling this
server to any specific IdP. Adding a new IdP to the main UI app does not
require any change here.

Email rendering is delegated to notifications-server's ``magic_link``
Jinja template — exactly the same template and call signature the main
Next.js app uses (see ``app/src/pages/api/auth/[...nextauth].ts``), so
copy and branding stay in lockstep.
"""

import logging
import os
import secrets
import threading
import time
from datetime import datetime, timedelta, timezone

import jwt
import requests
from fastapi import APIRouter, Form, Request
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse
from sqlalchemy import text

from benchmark_server.utils.db_utils import db_engine
from benchmark_server.utils.jwt_keys import load_private_key_tolerant

# Routes are mounted at absolute paths (no prefix). The email-callback path
# lives under ``/api/auth/callback/email`` to mirror the URL scheme used by
# the main app's NextAuth EmailProvider, keeping operator mental model
# identical across the two surfaces.
router = APIRouter(tags=["auth"])
logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Magic-link configuration
# ---------------------------------------------------------------------------
NOTIFICATION_SERVICE_URL = os.environ.get(
    "NOTIFICATION_SERVICE_URL", "http://notifications:80"
)
# 10 minutes mirrors the UI app's NextAuth EmailProvider maxAge.
MAGIC_LINK_TTL_SECONDS = int(os.environ.get("MAGIC_LINK_TTL_SECONDS", "600"))

# In-process token store: token -> (email, expires_at_unix). Survives until
# process restart, which is acceptable for an internal tool — the worst
# outcome of a restart is the user re-requests a link. If/when durability
# matters, swap for a DB row keyed by token (mirroring NextAuth's
# ``user_auths`` magic-link rows).
_magic_tokens: dict[str, tuple[str, float]] = {}
_magic_lock = threading.Lock()

# Per-email cooldown on /auth/email-login. The endpoint is unauthenticated
# and triggers a real email send via notifications-server — without a
# throttle an attacker can mailbox-bomb a known internal user (and burn
# notifications-server / SES quota). ``EMAIL_LOGIN_COOLDOWN_SEC`` enforces
# a minimum gap between successive sends to the same address. Tightening
# at the ingress (limit-rps / limit-rpm in nudgebee-infra#635) catches
# the per-IP burst case; this catches the per-target case where many IPs
# all spam the same email. In-memory + UVICORN_WORKERS=1 (set in
# Dockerfile) — multi-worker would defeat this just like it defeats the
# token store, and that's already documented there.
_EMAIL_LOGIN_COOLDOWN_SEC = int(os.environ.get("EMAIL_LOGIN_COOLDOWN_SEC", "60"))
_email_login_last: dict[str, float] = {}
_email_login_lock = threading.Lock()

# ---------------------------------------------------------------------------
# Session JWT signing (reuses NextAuth RSA key)
# ---------------------------------------------------------------------------
_JWT_KEY_RAW = os.environ.get("NEXTAUTH_PRIVATE_KEY", "").replace("\\n", "\n").strip()
_jwt_private_key = None
if _JWT_KEY_RAW:
    try:
        # Tolerant loader handles the cluster's non-canonical-d key by
        # reconstructing from (n, e, p, q) with a freshly-computed d.
        # See ``benchmark_server/utils/jwt_keys.py`` for the rationale.
        _jwt_private_key = load_private_key_tolerant(_JWT_KEY_RAW.encode())
    except Exception as e:
        logger.error("Failed to load JWT private key for session signing: %s", e)

SESSION_COOKIE = "benchmark_session"
SESSION_MAX_AGE = int(os.environ.get("SESSION_MAX_AGE_HOURS", "24")) * 3600
# Set the Secure flag on the session cookie unconditionally — every
# real deployment of this dashboard is HTTPS (TLS-terminated at the
# ingress). ``request.url.scheme`` would only return "https" when
# uvicorn is started with ``--proxy-headers``, which is NOT how the
# Dockerfile starts it; relying on that flag silently dropped Secure
# behind the cluster's TLS terminator and made session cookies
# steal-able over MITM. Set ``BENCHMARK_INSECURE_COOKIES=1`` only for
# local HTTP dev; production must never see it.
SESSION_COOKIE_SECURE = os.environ.get("BENCHMARK_INSECURE_COOKIES", "0") != "1"

# JWT audience / issuer for benchmark-minted session tokens. NEXTAUTH_PRIVATE_KEY
# is shared across NextAuth, api-server, and any other service signing with
# the same key — if we don't set+validate ``iss`` and ``aud`` an RS256 token
# minted for any of those (with a top-level ``user_id`` claim) would be
# accepted as a benchmark session. ``iss`` / ``aud`` close the cross-service
# token-confusion gap.
JWT_ISSUER = "benchmark-server"
JWT_AUDIENCE = "benchmark"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
def create_session_jwt(user_data: dict) -> str:
    """Create an RS256-signed JWT for the session cookie. Identity-only.

    Authorization (tenants / role / accounts) is NOT in the JWT — it's
    fetched from the api-server schema on demand via authz.get_user_authz.
    Keeping the cookie small means it stays under the 4KB browser limit
    even for users with many tenants/accounts, and RBAC changes don't
    require the user to re-log in to see them.
    """
    now = datetime.now(timezone.utc)
    payload = {
        # Identity only — anything else makes the JWT a stale snapshot.
        "user_id": user_data.get("user_id", ""),
        "email": user_data.get("email", ""),
        "display_name": user_data.get("display_name", ""),
        "iat": now,
        "exp": now + timedelta(seconds=SESSION_MAX_AGE),
        "iss": JWT_ISSUER,
        "aud": JWT_AUDIENCE,
    }
    return jwt.encode(payload, _jwt_private_key, algorithm="RS256")


def lookup_user_by_email(email: str) -> dict | None:
    """Look up a user by email — identity only.

    Returns ``user_id``, ``email``, ``display_name`` for an active user,
    or ``None`` if not found / inactive. Tenant / role / account
    resolution is intentionally NOT done here — that's authz, not
    identity, and lives in ``benchmark_server.middleware.authz`` so it
    can be re-fetched from DB rather than baked into the JWT.
    """
    if not db_engine:
        return None
    try:
        with db_engine.connect() as conn:
            row = conn.execute(
                text(
                    "SELECT u.id, u.username, u.display_name "
                    "FROM users u "
                    "WHERE u.username = :email AND u.status = 'active' "
                    "LIMIT 1"
                ),
                {"email": email},
            ).fetchone()
            if not row:
                return None
            return {
                "user_id": str(row[0]),
                "email": str(row[1]),
                "display_name": str(row[2]) if row[2] else email,
            }
    except Exception as e:
        logger.error("User lookup failed for %s: %s", email, e)
        return None


# ---------------------------------------------------------------------------
# Magic-link helpers
# ---------------------------------------------------------------------------
def _purge_expired_tokens() -> None:
    """Drop expired entries from the in-process magic-link store.

    Called opportunistically (on each issue / consume) to keep the dict
    bounded; for an internal tool with a handful of logins per day this
    is enough — no separate timer needed.
    """
    now = time.time()
    with _magic_lock:
        stale = [t for t, (_, exp) in _magic_tokens.items() if exp <= now]
        for t in stale:
            _magic_tokens.pop(t, None)


def _issue_magic_token(email: str) -> str:
    """Generate a one-time, URL-safe token for ``email`` and remember it."""
    _purge_expired_tokens()
    token = secrets.token_urlsafe(32)
    expires_at = time.time() + MAGIC_LINK_TTL_SECONDS
    with _magic_lock:
        _magic_tokens[token] = (email, expires_at)
    return token


def _consume_magic_token(token: str) -> str | None:
    """Pop ``token`` from the store and return the bound email if still
    valid, else ``None``. One-shot — a token is unusable after first use."""
    _purge_expired_tokens()
    with _magic_lock:
        entry = _magic_tokens.pop(token, None)
    if entry is None:
        return None
    email, expires_at = entry
    if expires_at <= time.time():
        return None
    return email


def _send_magic_link_email(email: str, magic_link_url: str) -> bool:
    """Ask notifications-server to render and send the ``magic_link``
    template. Returns True if the call succeeded.

    The call signature mirrors the main UI app's NextAuth EmailProvider
    (see ``app/src/pages/api/auth/[...nextauth].ts``) so the rendered
    email is identical across the two surfaces.
    """
    try:
        resp = requests.post(
            f"{NOTIFICATION_SERVICE_URL}/api/emails/send",
            json={
                "recipients": email,
                "subject": "Sign in to your account",
                "template": "magic_link",
                "template_params": {"magic_link_url": magic_link_url},
            },
            timeout=10,
        )
        if resp.status_code != 200:
            logger.error(
                "magic-link send failed: status=%s body=%s",
                resp.status_code,
                resp.text[:200],
            )
            return False
        return True
    except Exception as e:
        logger.exception("magic-link send raised: %s", e)
        return False


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------
_SIGNIN_HTML = """<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Sign in — Nudgebee Benchmarks</title>
  <link href="https://fonts.googleapis.com/css2?family=Roboto:wght@400;500;600&display=swap" rel="stylesheet">
  <style>
    * { box-sizing: border-box; }
    body { font-family: 'Roboto', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
           background: #ffffff; color: #374151; margin: 0; min-height: 100vh; }
    .layout { display: flex; min-height: 100vh; padding: 20px; gap: 0; }
    .panel-left { flex: 0 0 58%; border-radius: 24px;
                  background: radial-gradient(50% 50% at 50% 50%, #21375A 0%, #18273F 100%);
                  display: flex; align-items: center; justify-content: center;
                  position: relative; overflow: hidden; }
    .panel-left .brand { position: absolute; top: 40px; left: 0; right: 0;
                         text-align: center; }
    .panel-left .brand-logo { width: 220px; max-width: 60%; height: auto;
                              max-height: 72px; object-fit: contain; }
    .panel-left .brand-tag { color: #94A3B8; font-size: 11px; letter-spacing: 2px;
                             margin-top: 6px; text-transform: uppercase; }
    .illustration { width: 70%; max-width: 420px; max-height: 70vh;
                    margin-top: 120px; object-fit: contain; }
    .panel-right { flex: 1; display: flex; align-items: center; justify-content: center;
                   padding: 24px; }
    .form-wrap { width: 100%; max-width: 360px; }
    .top-icon { width: 64px; height: 64px; margin: 0 auto 24px;
                background: #FFF7DC; border-radius: 16px;
                display: flex; align-items: center; justify-content: center; }
    h1 { color: #374151; font-size: 32px; font-weight: 600; margin: 0; text-align: center; }
    .subtitle { color: #737373; font-size: 14px; text-align: center;
                margin: 8px 0 32px; }
    label { display: block; color: #374151; font-size: 14px; font-weight: 500;
            margin: 0 0 8px; }
    input[type=email] { width: 100%; padding: 12px 14px; height: 44px;
            border: 1px solid #D0D0D0; border-radius: 4px; color: #374151;
            font-size: 14px; font-family: 'Roboto', sans-serif; outline: none;
            background: #ffffff; }
    input[type=email]:focus { border-color: #FACF39; box-shadow: 0 0 0 3px rgba(250,207,57,0.15); }
    button { width: 100%; padding: 12px 16px; margin-top: 16px;
             background: #FACF39; color: #1F2937; border: 0; border-radius: 4px;
             font-family: 'Roboto', sans-serif; font-weight: 600; font-size: 14px;
             cursor: pointer; transition: background 0.15s ease; }
    button:hover { background: #E6BB33; }
    .hint { color: #737373; font-size: 12px; margin-top: 16px; text-align: center; }
    .alert { padding: 10px 12px; border-radius: 4px; font-size: 13px;
             margin-bottom: 16px; border: 1px solid; }
    .alert.ok { background: #ECFDF5; color: #065F46; border-color: #A7F3D0; }
    .alert.err { background: #FEF2F2; color: #991B1B; border-color: #FECACA; }
    @media (max-width: 768px) {
      .layout { padding: 0; flex-direction: column; }
      .panel-left { display: none; }
      .panel-right { min-height: 100vh; }
    }
  </style>
</head>
<body>
  <div class="layout">
    <div class="panel-left">
      <div class="brand">
        <img class="brand-logo" src="/static/welcome-text-logo.svg" alt="Nudgebee">
        <div class="brand-tag">Benchmarks</div>
      </div>
      <img class="illustration" src="/static/signin-illustration.png" alt="">
    </div>
    <div class="panel-right">
      <form class="form-wrap" method="post" action="/auth/email-login">
        <div class="top-icon">
          <svg width="32" height="32" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2zm0 4l8 5 8-5"
                  stroke="#FACF39" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
        <h1>Welcome back!</h1>
        <div class="subtitle">Sign in to the Nudgebee benchmark dashboard</div>
        __ALERT__
        <label for="email">Email Magic Link</label>
        <input id="email" name="email" type="email" required autofocus
               placeholder="you@example.com" autocomplete="email">
        <button type="submit">Send Magic Link</button>
        <div class="hint">Link expires in 10 minutes. Check your inbox.</div>
      </form>
    </div>
  </div>
</body>
</html>
"""


def _render_signin(alert_html: str = "") -> HTMLResponse:
    return HTMLResponse(_SIGNIN_HTML.replace("__ALERT__", alert_html))


@router.get("/signin")
async def signin_page(request: Request, sent: str = "", err: str = ""):
    """Render the email-input form."""
    alert = ""
    if sent:
        alert = (
            '<div class="alert ok">If that email is registered, a sign-in '
            "link is on its way. Check your inbox.</div>"
        )
    elif err:
        msgs = {
            "invalid_link": "That sign-in link is invalid or has expired.",
            "send_failed": "Could not send the email — try again in a moment.",
            "config": "Auth is not configured. Contact an administrator.",
        }
        alert = f'<div class="alert err">{msgs.get(err, "Sign-in failed.")}</div>'
    return _render_signin(alert)


@router.get("/auth/login")
async def login_redirect():
    """Back-compat: legacy clients that hit /auth/login land on /signin."""
    return RedirectResponse("/signin", status_code=302)


@router.post("/auth/email-login")
async def email_login(request: Request, email: str = Form("")):
    """Send a magic-link email if ``email`` belongs to an active user.

    Always responds 200 (redirect to /signin?sent=1) regardless of whether
    the address is registered — we don't want this endpoint to leak which
    addresses exist in the system.
    """
    if not _jwt_private_key:
        return RedirectResponse("/signin?err=config", status_code=302)

    normalized = (email or "").strip().lower()
    if not normalized:
        return RedirectResponse("/signin?err=invalid_link", status_code=302)

    # Per-email cooldown — checked BEFORE the user lookup so that a positive
    # check doesn't leak which emails are registered (timing-equivalent to
    # the unregistered branch below). Returns the same /signin?sent=1 either
    # way: caller can't distinguish "throttled" from "sent" or "unregistered".
    now = time.time()
    with _email_login_lock:
        last = _email_login_last.get(normalized, 0.0)
        if now - last < _EMAIL_LOGIN_COOLDOWN_SEC:
            logger.info(
                "magic-link cooldown: throttling %s (last=%.0fs ago, cooldown=%ds)",
                normalized,
                now - last,
                _EMAIL_LOGIN_COOLDOWN_SEC,
            )
            return RedirectResponse("/signin?sent=1", status_code=302)
        _email_login_last[normalized] = now

    user_data = lookup_user_by_email(normalized)
    if user_data is None:
        # Don't tell the caller — same response shape as the success path.
        logger.info(
            "magic-link request for unregistered email: %s (silent)", normalized
        )
        return RedirectResponse("/signin?sent=1", status_code=302)

    token = _issue_magic_token(normalized)
    base = str(request.base_url).rstrip("/")
    magic_link_url = f"{base}/api/auth/callback/email?token={token}"

    if not _send_magic_link_email(normalized, magic_link_url):
        # Drop the token so we don't leave a usable one floating after a
        # send failure (the user will retry; a fresh token will be issued).
        with _magic_lock:
            _magic_tokens.pop(token, None)
        # Also clear the cooldown entry so the user can immediately retry —
        # the send failure was on our side, not abuse.
        with _email_login_lock:
            _email_login_last.pop(normalized, None)
        return RedirectResponse("/signin?err=send_failed", status_code=302)

    logger.info("magic-link sent to %s", normalized)
    return RedirectResponse("/signin?sent=1", status_code=302)


@router.get("/api/auth/callback/email", name="auth_callback")
async def email_callback(request: Request, token: str = "", confirm: str = ""):
    """Validate a magic-link token, mint a session, and log the user in.

    Email security scanners (SafeLinks, Proofpoint, Mimecast) follow links
    in the inbox before the human ever clicks. To stop them from burning
    the one-shot token, the first GET serves a small HTML page that
    JavaScript-redirects to the same URL with ``confirm=1`` — scanners
    don't execute JS, so they get a 200 and stop. The real user's browser
    transparently follows the JS redirect and consumes the token. Mirrors
    the UI app's defense in ``[...nextauth].ts:1568-1591``.
    """
    # Validate token *shape* before doing anything else with it. ``token`` is
    # interpolated into HTML / JS in the scanner-shield interstitial below,
    # so an attacker-supplied value with HTML/JS metacharacters could break
    # out of the string literal and execute arbitrary JS in the dashboard's
    # origin (reflected XSS). secrets.token_urlsafe(32) only emits
    # [A-Za-z0-9_-] characters, so anything else means the request can't be
    # one we issued — reject before reflecting.
    import re as _re

    if not token or not _re.fullmatch(r"[A-Za-z0-9_-]{20,80}", token):
        return RedirectResponse("/signin?err=invalid_link", status_code=302)

    # Fail-closed if the JWT signing key is unavailable — without this,
    # the consume below would burn the one-shot token and then ``create_session_jwt``
    # would raise a 500, leaving the user unable to retry the same link.
    if not _jwt_private_key:
        return RedirectResponse("/signin?err=config", status_code=302)

    if confirm != "1":
        # Scanner shield: serve an HTML interstitial that JS-redirects.
        target = f"/api/auth/callback/email?token={token}&confirm=1"
        return HTMLResponse(
            "<!DOCTYPE html><html><head>"
            '<meta charset="utf-8"><title>Verifying...</title>'
            "</head><body><p>Verifying your sign-in link…</p>"
            f'<script>window.location.replace("{target}");</script>'
            f'<noscript><p><a href="{target}">Click to continue</a></p></noscript>'
            "</body></html>",
            headers={
                "Cache-Control": "no-store, no-cache, must-revalidate, private",
                "Pragma": "no-cache",
            },
        )

    email = _consume_magic_token(token)
    if email is None:
        return RedirectResponse("/signin?err=invalid_link", status_code=302)

    user_data = lookup_user_by_email(email)
    if user_data is None:
        # User existed at issue time but is gone/suspended now — no session.
        logger.warning("magic-link consumed but user no longer active: %s", email)
        return RedirectResponse("/signin?err=invalid_link", status_code=302)

    # Prime the authz cache so the first request after login doesn't pay
    # the 3 SQL queries inline. Failure here is non-fatal — get_user_authz
    # will fetch on demand on the next call.
    try:
        from benchmark_server.middleware.authz import get_user_authz

        authz = get_user_authz(user_data["user_id"], force_refresh=True)
        logger.info(
            "magic-link login: email=%s display_name=%s super_admin=%s tenants=%d",
            user_data["email"],
            user_data["display_name"],
            authz.is_super_admin,
            len(authz.tenants),
        )
    except Exception as e:
        logger.warning("authz prime failed for %s: %s", email, e)

    session_token = create_session_jwt(user_data)
    response = RedirectResponse("/", status_code=302)
    response.set_cookie(
        SESSION_COOKIE,
        session_token,
        httponly=True,
        # samesite="lax" is enough: the magic-link click navigates the
        # browser to this same-origin endpoint, so the freshly-set cookie
        # is sent on the immediate redirect to "/". CSRF defence on
        # state-changing endpoints is provided by the Origin/Referer check
        # in AuthMiddleware (see middleware/auth.py).
        samesite="lax",
        max_age=SESSION_MAX_AGE,
        secure=SESSION_COOKIE_SECURE,
    )
    return response


@router.get("/auth/me")
async def me(request: Request):
    """Return the authenticated user + their authz context.

    Shape:
      {
        user: { user_id, email, display_name },
        is_super_admin: bool,
        tenants: [ { id, role, account_count } ]
      }

    The dashboard uses ``tenants`` to populate the tenant dropdown on
    the Run Benchmark form. There is no "current tenant" — every list
    endpoint server-side filters by ``tenant_id IN authz.tenants``,
    and the user picks a target tenant per benchmark in the form.
    """
    user = getattr(request.state, "user", None)
    if not user:
        return JSONResponse(status_code=401, content={"detail": "Not authenticated"})

    from benchmark_server.middleware.authz import get_user_authz

    authz = get_user_authz(user.user_id)

    return {
        "user": {
            "user_id": user.user_id,
            "email": user.email,
            "display_name": getattr(user, "display_name", user.email),
        },
        "is_super_admin": authz.is_super_admin,
        "tenants": [
            {
                "id": ta.tenant_id,
                "role": ta.role,
                "account_count": len(ta.account_ids),
            }
            for ta in authz.tenants
        ],
    }


@router.post("/auth/refresh-authz")
async def refresh_authz(request: Request):
    """Force-refresh the cached authz for the logged-in user.

    Use when api-server's RBAC has changed and the user wants the
    benchmark to pick up the change without logging out and back in.
    """
    user = getattr(request.state, "user", None)
    if not user:
        return JSONResponse(status_code=401, content={"detail": "Not authenticated"})

    from benchmark_server.middleware.authz import get_user_authz

    authz = get_user_authz(user.user_id, force_refresh=True)
    return {
        "is_super_admin": authz.is_super_admin,
        "tenant_count": len(authz.tenants),
    }


@router.get("/auth/logout")
async def logout(request: Request):
    """Clear the session and cached authz.

    Authz invalidation is important: a user may log back in with a
    different account on the same machine, and we don't want them to
    inherit the previous user's cached tenant list.
    """
    user = getattr(request.state, "user", None)
    if user:
        from benchmark_server.middleware.authz import invalidate_authz

        invalidate_authz(user.user_id)

    response = RedirectResponse("/signin")
    response.delete_cookie(SESSION_COOKIE)
    return response
