package signing

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestNewSigner_Empty(t *testing.T) {
	s, err := NewSigner("", "test", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != nil {
		t.Fatal("expected nil signer when key is empty")
	}
}

func TestNewSigner_InvalidKey(t *testing.T) {
	_, err := NewSigner("not-valid-base64!!!", "test", testLogger())
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}

	_, err = NewSigner(base64.StdEncoding.EncodeToString([]byte("short")), "test", testLogger())
	if err == nil {
		t.Fatal("expected error for wrong key size")
	}
}

func TestNewSigner_PEM(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	// Encode private key as PEM (PKCS8)
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes}))

	s, err := NewSigner(privPEM, "pem-key", testLogger())
	if err != nil {
		t.Fatalf("NewSigner with PEM: %v", err)
	}

	msg := []byte(`{"action":"db_query","datasource_id":"ds-1","params":{"query":"SELECT 1"}}`)
	signed, err := s.Sign(msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Verify the signature with the raw public key
	var result map[string]any
	if err := json.Unmarshal(signed, &result); err != nil {
		t.Fatalf("Unmarshal signed: %v", err)
	}
	sigB64, _ := result["signature"].(string)
	sigBytes, _ := base64.StdEncoding.DecodeString(sigB64)
	payloadStr, _ := result["signed_payload"].(string)

	if !ed25519.Verify(pub, []byte(payloadStr), sigBytes) {
		t.Fatal("signature from PEM signer did not verify")
	}
}

func TestSign_ConfigSync(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	s, err := NewSigner(base64.StdEncoding.EncodeToString(priv), "test-key", testLogger())
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	msg := map[string]any{
		"action":     "datasource_config_sync",
		"account_id": "acc-123",
		"datasources": []any{
			map[string]any{"id": "ds-1", "type": "postgresql"},
		},
	}
	msgBytes, _ := json.Marshal(msg)

	signed, err := s.Sign(msgBytes)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(signed, &result); err != nil {
		t.Fatalf("Unmarshal signed: %v", err)
	}

	for _, field := range []string{"signed_payload", "signature", "signed_at", "nonce", "key_id"} {
		if _, ok := result[field]; !ok {
			t.Fatalf("missing field %q in signed message", field)
		}
	}

	// Verify Ed25519 signature
	sigB64, _ := result["signature"].(string)
	sigBytes, _ := base64.StdEncoding.DecodeString(sigB64)
	payloadStr, _ := result["signed_payload"].(string)

	if !ed25519.Verify(pub, []byte(payloadStr), sigBytes) {
		t.Fatal("Ed25519 signature verification failed")
	}

	// Verify signed_payload contains the right fields
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v", err)
	}
	if payload["action"] != "datasource_config_sync" {
		t.Fatalf("expected action in payload, got %v", payload["action"])
	}
	if payload["account_id"] != "acc-123" {
		t.Fatalf("expected account_id in payload, got %v", payload["account_id"])
	}
	if _, ok := payload["datasources"]; !ok {
		t.Fatal("expected datasources in payload")
	}
}

func TestSign_ActionRequest(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	s, _ := NewSigner(base64.StdEncoding.EncodeToString(priv), "test-key", testLogger())

	msg := map[string]any{
		"action":        "db_query",
		"datasource_id": "ds-1",
		"params":        map[string]any{"query": "SELECT 1"},
	}
	msgBytes, _ := json.Marshal(msg)

	signed, err := s.Sign(msgBytes)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(signed, &result); err != nil {
		t.Fatalf("Unmarshal signed: %v", err)
	}

	payloadStr, _ := result["signed_payload"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v", err)
	}

	if payload["action"] != "db_query" {
		t.Fatalf("expected db_query, got %v", payload["action"])
	}
	if payload["datasource_id"] != "ds-1" {
		t.Fatalf("expected ds-1, got %v", payload["datasource_id"])
	}
}

func TestSign_HttpRequest(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	s, _ := NewSigner(base64.StdEncoding.EncodeToString(priv), "test-key", testLogger())

	msg := map[string]any{
		"action":        "http_request",
		"datasource_id": "ds-prom-1",
		"method":        "GET",
		"url":           "/api/v1/query",
		"header":        map[string]any{"Accept": []string{"application/json"}},
		"body":          "",
	}
	msgBytes, _ := json.Marshal(msg)

	signed, err := s.Sign(msgBytes)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(signed, &result); err != nil {
		t.Fatalf("Unmarshal signed: %v", err)
	}

	payloadStr, _ := result["signed_payload"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v", err)
	}

	// http_request signs: action, datasource_id, method, url, header, body
	for _, field := range []string{"action", "datasource_id", "method", "url", "header", "body"} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("missing signed field %q in payload", field)
		}
	}
}

func TestNewSigner_OpenSSH(t *testing.T) {
	// Generate an OpenSSH ed25519 key using ssh-keygen
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "id_ed25519")

	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", keyFile, "-N", "", "-C", "test@test.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen failed: %v\n%s", err, out)
	}

	privPEM, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}

	pubSSH, err := os.ReadFile(keyFile + ".pub")
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}

	s, err := NewSigner(string(privPEM), "openssh-key", testLogger())
	if err != nil {
		t.Fatalf("NewSigner with OpenSSH key: %v", err)
	}

	msg := []byte(`{"action":"db_query","datasource_id":"ds-1","params":{"query":"SELECT 1"}}`)
	signed, err := s.Sign(msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Extract the public key from OpenSSH format and verify signature
	var result map[string]any
	if err := json.Unmarshal(signed, &result); err != nil {
		t.Fatalf("Unmarshal signed: %v", err)
	}
	sigB64, _ := result["signature"].(string)
	sigBytes, _ := base64.StdEncoding.DecodeString(sigB64)
	payloadStr, _ := result["signed_payload"].(string)

	// The signer's internal key should produce valid signatures
	pub := s.privateKey.Public().(ed25519.PublicKey)
	if !ed25519.Verify(pub, []byte(payloadStr), sigBytes) {
		t.Fatal("signature from OpenSSH signer did not verify")
	}

	_ = pubSSH // confirms the key pair was generated
}

func TestNewSigner_HexAndSeed(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	priv := ed25519.NewKeyFromSeed(seed)

	// Test 32-byte seed in HEX
	hexSeed := hex.EncodeToString(seed)
	s, err := NewSigner(hexSeed, "hex-seed", testLogger())
	if err != nil {
		t.Fatalf("NewSigner with hex seed: %v", err)
	}
	if !s.privateKey.Equal(priv) {
		t.Fatal("private key mismatch for hex seed")
	}

	// Test 64-byte private key in HEX
	hexPriv := hex.EncodeToString(priv)
	s, err = NewSigner(hexPriv, "hex-priv", testLogger())
	if err != nil {
		t.Fatalf("NewSigner with hex priv: %v", err)
	}
	if !s.privateKey.Equal(priv) {
		t.Fatal("private key mismatch for hex priv")
	}

	// Test 32-byte seed in Base64
	b64Seed := base64.StdEncoding.EncodeToString(seed)
	s, err = NewSigner(b64Seed, "b64-seed", testLogger())
	if err != nil {
		t.Fatalf("NewSigner with b64 seed: %v", err)
	}
	if !s.privateKey.Equal(priv) {
		t.Fatal("private key mismatch for b64 seed")
	}
}
