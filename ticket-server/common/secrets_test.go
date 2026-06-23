package common

import "testing"

// testAESKey is a 32-byte (AES-256) key encoded as 64 hex chars.
const testAESKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func withEncryptionKey(t *testing.T, key string) {
	t.Helper()
	original := Config.NudgebeeEncryptionKey
	Config.NudgebeeEncryptionKey = key
	t.Cleanup(func() { Config.NudgebeeEncryptionKey = original })
}

func TestEncode(t *testing.T) {
	// SHA-1 of "hello" is a well-known fixed digest.
	const want = "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
	if got := Encode("hello"); got != want {
		t.Errorf("Encode(\"hello\") = %q, want %q", got, want)
	}
	// Encode is deterministic — calling it twice on the same input yields the
	// same digest. Assigning to locals first avoids staticcheck SA4000
	// (identical expressions on both sides of '!=').
	first := Encode("x")
	second := Encode("x")
	if first != second {
		t.Error("Encode is not deterministic for the same input")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	withEncryptionKey(t, testAESKey)

	for _, msg := range []string{"", "hello world", "p@ssw0rd-with-symbols!#$"} {
		enc, err := Encrypt(msg)
		if err != nil {
			t.Fatalf("Encrypt(%q) error: %v", msg, err)
		}
		dec, err := Decrypt(enc)
		if err != nil {
			t.Fatalf("Decrypt() error for %q: %v", msg, err)
		}
		if dec != msg {
			t.Errorf("round-trip mismatch: got %q, want %q", dec, msg)
		}
	}
}

func TestDecryptStripsEncPrefix(t *testing.T) {
	withEncryptionKey(t, testAESKey)

	enc, err := Encrypt("payload")
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	dec, err := Decrypt("enc:" + enc)
	if err != nil {
		t.Fatalf("Decrypt with enc: prefix error: %v", err)
	}
	if dec != "payload" {
		t.Errorf("got %q, want %q", dec, "payload")
	}
}

func TestEncryptProducesDistinctCiphertexts(t *testing.T) {
	withEncryptionKey(t, testAESKey)

	a, err := Encrypt("same")
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	b, err := Encrypt("same")
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	if a == b {
		t.Error("expected distinct ciphertexts for repeated encryption (random nonce)")
	}
}

func TestDecryptTooShort(t *testing.T) {
	withEncryptionKey(t, testAESKey)

	// 10 bytes (20 hex chars) — below the 12-byte IV + 16-byte tag minimum.
	if _, err := Decrypt("00112233445566778899"); err == nil {
		t.Error("expected error for too-short ciphertext, got nil")
	}
}
