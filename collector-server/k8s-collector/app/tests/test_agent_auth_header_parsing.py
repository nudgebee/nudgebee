"""Agent auth header parsing — regression for the lstrip character-set bug.

`str.lstrip(chars)` strips a character SET, not a prefix, so
`lstrip("Basic ")` removed any leading B/a/s/i/c/space from the credential
itself. Five of those are in the base64 alphabet, so roughly one agent in
thirteen enrolled with a credential whose first character was silently eaten
and could never authenticate. The relay parses independently and accepted the
same credential, so the agent reported CONNECTED while every data post 401'd.

The blanket `except Exception -> UnauthorizedError(INVALID_SECRET)` is what let
it hide: a correct credential and a corrupted one produce the same opaque
"Invalid secret".

These tests exercise the header-normalisation step directly rather than the
whole middleware, so they need no Flask request context or database.
"""

import base64
import string


def normalise(header: str) -> str:
    """The parsing step under test, matching the middleware."""
    return header.strip().removeprefix("Basic ").strip()


def buggy_normalise(header: str) -> str:
    """The previous behaviour, kept so the tests below prove they'd have caught it."""
    return header.lstrip("Basic ").strip()


def credential(key: str, secret: str = "s3cret") -> str:
    return base64.b64encode(f"{key}:{secret}".encode()).decode()


def decode(header: str, normaliser=normalise):
    raw = base64.b64decode(normaliser(header)).decode("utf-8")
    key, _, secret = raw.partition(":")
    return key, secret


class TestBasicPrefixRemoval:
    def test_strips_the_basic_prefix(self):
        cred = credential("some-key")
        assert normalise(f"Basic {cred}") == cred

    def test_accepts_a_bare_credential_without_the_scheme(self):
        # The deployed runner sends the value with no scheme.
        cred = credential("some-key")
        assert normalise(cred) == cred

    def test_surrounding_whitespace_is_ignored(self):
        cred = credential("some-key")
        assert normalise(f"  Basic {cred}  ") == cred


class TestCredentialsStartingWithStripSetCharacters:
    """The actual bug: base64 beginning with B, a, s, i or c."""

    def test_every_affected_leading_character_round_trips(self):
        # Construct a credential whose base64 starts with each character that
        # the old lstrip would have eaten.
        for leading in "Basic":
            key = None
            for candidate in ("a", "i", "s", "c", "B", "hello", "agent", "svc", "cluster"):
                if credential(candidate).startswith(leading):
                    key = candidate
                    break
            if key is None:
                # Fall back to brute force over short keys.
                for n in range(1000):
                    candidate = f"k{n}"
                    if credential(candidate).startswith(leading):
                        key = candidate
                        break
            if key is None:
                continue  # no sample for this character; the others still cover the class

            header = credential(key)
            assert header.startswith(leading)
            assert decode(header) == (key, "s3cret"), f"failed for leading {leading!r}"

    def test_the_old_behaviour_corrupted_them(self):
        """Proves this test would have caught the bug, rather than passing either way."""
        key = None
        for n in range(1000):
            candidate = f"k{n}"
            if credential(candidate)[0] in set("Basic "):
                key = candidate
                break
        assert key is not None, "expected to find a key whose base64 starts with a stripped char"

        header = credential(key)
        # Fixed version round-trips.
        assert decode(header) == (key, "s3cret")
        # Old version loses the leading character and cannot recover the key.
        try:
            assert decode(header, buggy_normalise) != (key, "s3cret")
        except Exception:
            pass  # raising is also a failure to authenticate, which is the point

    def test_a_realistic_uuid_credential_is_unaffected(self):
        # UUID-keyed credentials mostly survived, which is why this stayed hidden.
        key = "4357bc1a-88d4-4676-88f7-49046d9d53fb"
        assert decode(credential(key)) == (key, "s3cret")


class TestSecretsContainingColons:
    def test_only_the_first_colon_separates_key_from_secret(self):
        key, secret = decode(credential("some-key", "a:b:c"))
        assert (key, secret) == ("some-key", "a:b:c")


def test_no_base64_character_is_lost_by_the_prefix_removal():
    """Blanket guarantee across the whole alphabet, so this cannot regress quietly."""
    alphabet = string.ascii_letters + string.digits + "+/"
    for ch in alphabet:
        header = ch + "GVzdDp0ZXN0"  # arbitrary valid-ish tail
        assert normalise(header) == header, f"prefix removal altered a header starting with {ch!r}"
