"""Tests for the tolerant RSA private-key loader.

The cluster's ``NEXTAUTH_PRIVATE_KEY`` has a non-canonical ``d`` field
that ``cryptography >= 45`` rejects at load time. The loader's job is
to fall back to ``unsafe_skip_rsa_key_validation=True`` so signing
still works. These tests exercise both paths end-to-end via sign +
verify, including a cross-public-key check that catches a parser
regression even though we no longer hand-roll a parser.
"""

import base64
import logging

import jwt as pyjwt
import pytest
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec, rsa


def _good_pem() -> bytes:
    """Generate a fresh, canonical RSA-2048 private key PEM."""
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    return key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    )


def _make_pem_with_corrupt_d() -> bytes:
    """Forge a PKCS#8 PEM whose private exponent ``d`` field is bogus.

    Mirrors the cluster's broken state — ``p`` and ``q`` are correct so
    CRT-based signing works, but ``d`` doesn't satisfy
    ``e * d ≡ 1 (mod λ(n))``. ``cryptography 46`` will refuse strict-load
    this with ``ValueError("Invalid private key")``.

    Build approach: take a valid PEM, locate the ``d`` INTEGER in the
    inner PKCS#1 RSAPrivateKey SEQUENCE, replace its bytes with a
    same-length garbage value, re-armor.
    """
    pem = _good_pem()
    body = pem.split(b"-----BEGIN PRIVATE KEY-----")[1].split(b"-----END")[0].strip()
    der = bytearray(base64.b64decode(body))

    def _length_at(buf, off):
        b = buf[off]
        if b < 0x80:
            return b, off + 1
        n = b & 0x7F
        return int.from_bytes(buf[off + 1 : off + 1 + n], "big"), off + 1 + n

    def _skip_tlv(buf, off):
        off += 1
        length, off = _length_at(buf, off)
        return off + length

    # Outer PKCS#8 SEQUENCE: skip version + AlgID, descend into OCTET STRING
    off = 1
    _, off = _length_at(der, off)
    off = _skip_tlv(der, off)  # version
    off = _skip_tlv(der, off)  # AlgID
    assert der[off] == 0x04  # OCTET STRING
    off += 1
    _, inner_start = _length_at(der, off)

    # Inner SEQUENCE: skip version, n, e — d is next
    ioff = inner_start + 1
    _, ioff = _length_at(der, ioff)
    ioff = _skip_tlv(der, ioff)  # version
    ioff = _skip_tlv(der, ioff)  # n
    ioff = _skip_tlv(der, ioff)  # e

    assert der[ioff] == 0x02  # INTEGER (d)
    d_length, d_value_off = _length_at(der, ioff + 1)
    # Replace value bytes with same-length garbage (leading 0x00 keeps it a
    # positive DER INTEGER). cryptography 46 will reject this on strict
    # load because e*d ≢ 1 (mod λ(n)).
    der[d_value_off : d_value_off + d_length] = b"\x00" + b"\xab" * (d_length - 1)

    new_body = base64.encodebytes(bytes(der)).strip()
    return b"-----BEGIN PRIVATE KEY-----\n" + new_body + b"\n-----END PRIVATE KEY-----\n"


# ---------- Happy path ----------


def test_loads_canonical_key_strictly(caplog):
    """A normally-generated key takes the strict path; no warning fired.

    This is the regression assertion: if a future cryptography release
    starts rejecting canonical keys, or if our fallback starts firing
    for clean keys, this test fails loudly.
    """
    from benchmark_server.utils.jwt_keys import load_private_key_tolerant

    pem = _good_pem()
    with caplog.at_level(logging.WARNING, logger="benchmark_server.utils.jwt_keys"):
        k = load_private_key_tolerant(pem)
    assert isinstance(k, rsa.RSAPrivateKey)
    assert k.key_size == 2048
    assert not any(
        "failed strict load" in r.message for r in caplog.records
    ), "Fallback path fired for a canonical key — regression"


# ---------- Fallback path ----------


def test_loads_corrupt_d_via_fallback(caplog):
    """A key with junk ``d`` loads via the fallback path AND the warning
    fires. The caplog assertion is the new-code-is-exercised guard:
    if a future cryptography release relaxes the strict check, this
    test fails loudly (rather than passing silently with the strict
    path doing all the work)."""
    from benchmark_server.utils.jwt_keys import load_private_key_tolerant

    pem = _make_pem_with_corrupt_d()
    with caplog.at_level(logging.WARNING, logger="benchmark_server.utils.jwt_keys"):
        k = load_private_key_tolerant(pem)
    assert isinstance(k, rsa.RSAPrivateKey)
    assert k.key_size == 2048
    assert any(
        "failed strict load" in r.message for r in caplog.records
    ), "Expected fallback-path warning; if missing, the strict path silently handled the corrupt PEM"


def test_corrupt_d_pem_fails_strict_load():
    """Sanity check on the test fixture: ``load_pem_private_key`` directly
    rejects the forged PEM. If this ever stops failing, the fallback
    path becomes untested — alert the maintainer."""
    from cryptography.hazmat.primitives.serialization import load_pem_private_key

    pem = _make_pem_with_corrupt_d()
    with pytest.raises(ValueError):
        load_pem_private_key(pem, password=None)


# ---------- Interop: signatures from fallback-loaded key verify against ----------
# the public key derived **independently from the original PEM**


def test_fallback_key_interops_with_original_public_key():
    """A signature made with the fallback-loaded private key must verify
    against the public key extracted directly from the ORIGINAL (broken)
    PEM via openssl — not against the public key of the loaded private
    key (which would be a self-consistent tautology). This is the
    asymmetric check that catches a hypothetical p/q-swap-style
    regression even though we don't hand-roll a parser anymore.
    """
    import subprocess
    import tempfile
    from cryptography.hazmat.primitives.serialization import load_pem_public_key
    from benchmark_server.utils.jwt_keys import load_private_key_tolerant

    pem = _make_pem_with_corrupt_d()
    priv = load_private_key_tolerant(pem)

    # Independent public-key extraction: openssl pkey from the ORIGINAL
    # broken PEM. ``openssl pkey -pubout`` skips d-canonicality checks
    # and returns SubjectPublicKeyInfo built from (n, e) only.
    with tempfile.NamedTemporaryFile("wb", delete=False, suffix=".pem") as f:
        f.write(pem)
        original_path = f.name
    proc = subprocess.run(
        ["openssl", "pkey", "-in", original_path, "-pubout"],
        capture_output=True,
        check=True,
    )
    original_pub = load_pem_public_key(proc.stdout)

    token = pyjwt.encode(
        {"user_id": "u-1", "iss": "benchmark-server", "aud": "benchmark"},
        priv,
        algorithm="RS256",
    )
    decoded = pyjwt.decode(
        token,
        original_pub,
        algorithms=["RS256"],
        issuer="benchmark-server",
        audience="benchmark",
    )
    assert decoded["user_id"] == "u-1"


# ---------- Failure modes ----------


def test_rejects_non_rsa_key():
    """A non-RSA key (EC) must be rejected with a clear ValueError.
    Benchmark auth uses RS256; the loader must not silently accept
    other key types."""
    from benchmark_server.utils.jwt_keys import load_private_key_tolerant

    ec_key = ec.generate_private_key(ec.SECP256R1())
    ec_pem = ec_key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    )
    with pytest.raises(ValueError, match="expected RSAPrivateKey"):
        load_private_key_tolerant(ec_pem)


def test_rejects_garbage_pem():
    """Random bytes that aren't a valid PEM raise ValueError (not e.g. a
    cryptic UnsupportedAlgorithm or other internal exception type)."""
    from benchmark_server.utils.jwt_keys import load_private_key_tolerant

    with pytest.raises(ValueError):
        load_private_key_tolerant(b"not a pem at all")


def test_propagates_non_d_failures_unchanged():
    """Failures unrelated to non-canonical ``d`` must propagate with
    their original error message — the fallback should not mask
    'encrypted PEM' or 'malformed DER' as a generic d-recovery failure."""
    from benchmark_server.utils.jwt_keys import load_private_key_tolerant

    # Truncate a valid PEM mid-body. Strict load raises ValueError but
    # the message is NOT "Invalid private key" — it's a parse error.
    pem = _good_pem()
    body = pem.split(b"-----BEGIN PRIVATE KEY-----")[1].split(b"-----END")[0].strip()
    truncated = (
        b"-----BEGIN PRIVATE KEY-----\n" + body[: len(body) // 2] + b"\n-----END PRIVATE KEY-----\n"
    )
    with pytest.raises(ValueError) as exc:
        load_private_key_tolerant(truncated)
    # Must NOT be the fallback's "rotation needed" message — should be
    # cryptography's original parse-error message bubbling up.
    assert "Invalid private key" not in str(exc.value) or "PEM" in str(exc.value)
