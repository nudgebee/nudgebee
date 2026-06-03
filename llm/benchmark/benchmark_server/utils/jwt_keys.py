"""Tolerant RSA private-key loader for the shared NEXTAUTH_PRIVATE_KEY.

The cluster's ``NEXTAUTH_PRIVATE_KEY`` was generated with a non-canonical
private exponent ``d`` — i.e. the stored ``d`` does not satisfy
``e * d ≡ 1 (mod λ(n))``. This is irrelevant in practice because every
real RSA implementation (jose / Node.js, openssl, pyjwt + cryptography
< 45) signs via CRT and only uses ``p, q, dmp1, dmq1, iqmp``. But
``cryptography >= 45`` enforces the canonicality of ``d`` *at load time*
and raises ``ValueError("Invalid private key")`` for keys like this one.
Result: the benchmark server's auth modules can't load the cluster key,
even though the UI app's NextAuth (jose) loads and uses it without
issue.

``cryptography >= 39`` ships ``unsafe_skip_rsa_key_validation=True`` on
``load_pem_private_key`` (added in pyca/cryptography#8923) for exactly
this case — it skips the d-canonicality check while keeping every
other DER / OID / structural check. We use it as a one-line fallback;
the resulting key signs to bit-identical signatures as the original
(both use the original PEM's ``p, q, dmp1, dmq1, iqmp`` for CRT
signing), so it interoperates with everything the UI app signs and
verifies.

The fallback is gated on the **specific** "Invalid private key" error
message — any other failure mode (encrypted PEM, EC/DSA/Ed25519 key,
malformed DER, unsupported algorithm) still surfaces with its original
error so operator misconfiguration isn't masked.

Remove this fallback once the cluster's ``NEXTAUTH_PRIVATE_KEY`` is
rotated to a canonical key. Until then it's the only thing keeping
benchmark-server auth working — the startup warning makes the
broken-key state visible in cluster logs without leaking ticket
references into operational output.
"""

import logging

from cryptography.exceptions import UnsupportedAlgorithm
from cryptography.hazmat.primitives.asymmetric.rsa import RSAPrivateKey
from cryptography.hazmat.primitives.serialization import load_pem_private_key

logger = logging.getLogger(__name__)

# Substring of the ValueError raised by cryptography 45+ when the stored
# ``d`` doesn't satisfy ``e * d ≡ 1 (mod λ(n))``. We match on this
# specific message so other failure modes (encrypted PEM, malformed
# DER, etc.) surface unchanged instead of being masked by the fallback.
_NON_CANONICAL_D_ERROR = "Invalid private key"


def load_private_key_tolerant(pem_bytes: bytes) -> RSAPrivateKey:
    """Load an RSA private key, falling back to ``unsafe_skip_rsa_key_validation``
    when the stored ``d`` is non-canonical.

    Behavior:
      - **Happy path**: ``load_pem_private_key`` strict-load succeeds and
        returns an RSA key → return it. No fallback overhead.
      - **Non-RSA key**: strict load returns EC / Ed25519 / DSA → raise
        ``ValueError`` (benchmark auth uses RS256, key must be RSA).
      - **Non-canonical d**: strict load raises
        ``ValueError("Invalid private key")`` → retry with
        ``unsafe_skip_rsa_key_validation=True``. Logs a warning so the
        broken-key state is visible in cluster logs.
      - **Any other failure** (encrypted PEM, malformed DER, EC PKCS#8
        with unrecognised OID, etc.) → propagate unchanged so operator
        misconfig isn't silently absorbed by the fallback.
    """
    try:
        key = load_pem_private_key(pem_bytes, password=None)
    except ValueError as primary_err:
        if _NON_CANONICAL_D_ERROR not in str(primary_err):
            # Not the broken-d case — propagate as-is so the operator
            # sees the real reason (encrypted PEM, truncated DER, etc.)
            # rather than a generic "fallback failed" message.
            raise
        logger.warning(
            "RSA private key failed strict load (%s); retrying with "
            "unsafe_skip_rsa_key_validation=True. Stored 'd' is non-canonical "
            "but CRT params are valid, so signing still works.",
            primary_err,
        )
        key = load_pem_private_key(pem_bytes, password=None, unsafe_skip_rsa_key_validation=True)
    except UnsupportedAlgorithm as e:
        # PKCS#8 with an unsupported algorithm OID (e.g., a future
        # post-quantum scheme). Not recoverable here.
        raise ValueError(f"Unsupported key algorithm: {e}") from e

    if not isinstance(key, RSAPrivateKey):
        # cryptography returns the narrowest matching type. Benchmark
        # auth signs RS256 — anything else is an operator misconfig.
        raise ValueError(
            f"NEXTAUTH_PRIVATE_KEY is a {type(key).__name__}, expected RSAPrivateKey. "
            "Benchmark auth uses RS256 — the key must be RSA."
        )
    return key
