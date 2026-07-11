package signing

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"golang.org/x/crypto/ssh"
)

// Signer signs messages with an Ed25519 private key before forwarding to agents.
// The forager agent verifies these signatures to ensure messages originated from a trusted source.
type Signer struct {
	privateKey ed25519.PrivateKey
	keyID      string
	logger     *slog.Logger
}

// NewSigner creates a message signer from an Ed25519 private key.
// Accepts PEM-encoded PKCS8, OpenSSH private key, hex, or base64-encoded raw key.
// If privateKeyStr is empty, returns nil (signing disabled).
func NewSigner(privateKeyStr, keyID string, logger *slog.Logger) (*Signer, error) {
	if privateKeyStr == "" {
		logger.Warn("message signing disabled: no SIGNING_PRIVATE_KEY configured")
		return nil, nil
	}

	privKey, err := parsePrivateKey(privateKeyStr)
	if err != nil {
		return nil, fmt.Errorf("invalid SIGNING_PRIVATE_KEY: %w", err)
	}

	logger.Info("message signing enabled", "key_id", keyID)
	return &Signer{
		privateKey: privKey,
		keyID:      keyID,
		logger:     logger,
	}, nil
}

// parsePrivateKey tries OpenSSH PEM, PKCS8 PEM, HEX, then raw base64.
// Supports both 32-byte seeds and 64-byte private keys.
func parsePrivateKey(s string) (ed25519.PrivateKey, error) {
	s = strings.TrimSpace(s)

	// 1. Try PEM formats
	block, _ := pem.Decode([]byte(s))
	if block != nil {
		// 1a. OpenSSH format (ssh-keygen output)
		if block.Type == "OPENSSH PRIVATE KEY" {
			rawKey, err := ssh.ParseRawPrivateKey([]byte(s))
			if err != nil {
				return nil, fmt.Errorf("OpenSSH parse failed: %w", err)
			}
			edKey, ok := rawKey.(*ed25519.PrivateKey)
			if !ok {
				return nil, fmt.Errorf("OpenSSH key is not Ed25519")
			}
			return *edKey, nil
		}

		// 1b. PKCS8 format
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("PEM parse failed: %w", err)
		}
		edKey, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PEM key is not Ed25519")
		}
		return edKey, nil
	}

	// 2. Try HEX if it looks like HEX (64 or 128 chars)
	if len(s) == 64 || len(s) == 128 {
		if keyBytes, err := hex.DecodeString(s); err == nil {
			if len(keyBytes) == ed25519.SeedSize {
				return ed25519.NewKeyFromSeed(keyBytes), nil
			}
			if len(keyBytes) == ed25519.PrivateKeySize {
				return ed25519.PrivateKey(keyBytes), nil
			}
		}
	}

	// 3. Try raw base64
	keyBytes, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}

	if len(keyBytes) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(keyBytes), nil
	}
	if len(keyBytes) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(keyBytes), nil
	}

	return nil, fmt.Errorf("invalid key size: expected %d (seed) or %d (private key) bytes, got %d",
		ed25519.SeedSize, ed25519.PrivateKeySize, len(keyBytes))
}

// SigningFields defines which message fields to include in the signed payload per action.
// These must match the forager agent's signing field definitions exactly.
var SigningFields = map[string][]string{
	"datasource_config_sync": {"action", "account_id", "datasources"},

	"db_query":    {"action", "datasource_id", "params"},
	"db_execute":  {"action", "datasource_id", "params"},
	"db_metadata": {"action", "datasource_id", "params"},

	"ssh_command":  {"action", "datasource_id", "params"},
	"ssh_upload":   {"action", "datasource_id", "params"},
	"ssh_download": {"action", "datasource_id", "params"},
	"ssh_list_dir": {"action", "datasource_id", "params"},

	"http_request":    {"action", "datasource_id", "method", "url", "header", "body"},
	"mcp_request":     {"action", "datasource_id", "params"},
	"redis_command":   {"action", "datasource_id", "params"},
	"mongo_query":     {"action", "datasource_id", "params"},
	"mongo_aggregate": {"action", "datasource_id", "params"},

	// Dedicated read-only MongoDB diagnostics (forager exposes one action each,
	// parameter-less aside from the datasource).
	"mongo_server_status": {"action", "datasource_id", "params"},
	"mongo_repl_status":   {"action", "datasource_id", "params"},
	"mongo_current_ops":   {"action", "datasource_id", "params"},
}

// DefaultSigningFields is used when the action is not in SigningFields.
var DefaultSigningFields = []string{"action", "datasource_id", "params"}

// Sign adds signature metadata to a JSON message.
//
// It extracts the security-critical fields (based on the action), creates a
// canonical signed_payload, signs it, and adds {signed_payload, signature,
// signed_at, nonce, key_id} to the message.
func (s *Signer) Sign(msg []byte) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(msg, &raw); err != nil {
		return nil, fmt.Errorf("sign: invalid JSON input: %w", err)
	}

	// Determine which fields to sign based on action
	var action string
	if actionRaw, ok := raw["action"]; ok {
		if err := json.Unmarshal(actionRaw, &action); err != nil {
			s.logger.Warn("failed to unmarshal action field, using default signing fields", "err", err)
		}
	}

	fields := DefaultSigningFields
	if f, ok := SigningFields[action]; ok {
		fields = f
	}

	// Extract the fields to sign
	signPayload := make(map[string]json.RawMessage)
	for _, field := range fields {
		if val, ok := raw[field]; ok {
			signPayload[field] = val
		}
	}

	// Create canonical signed_payload JSON
	signedPayloadBytes, err := json.Marshal(signPayload)
	if err != nil {
		return nil, fmt.Errorf("sign: marshal signed payload: %w", err)
	}
	signedPayloadStr := string(signedPayloadBytes)

	// Sign the canonical payload
	sig := ed25519.Sign(s.privateKey, signedPayloadBytes)

	// Add signature metadata to the message
	raw["signed_payload"], _ = json.Marshal(signedPayloadStr)
	raw["signature"], _ = json.Marshal(base64.StdEncoding.EncodeToString(sig))
	raw["signed_at"], _ = json.Marshal(time.Now().UTC().Format(time.RFC3339))
	raw["nonce"], _ = json.Marshal(uuid.NewString())
	if s.keyID != "" {
		raw["key_id"], _ = json.Marshal(s.keyID)
	}

	return json.Marshal(raw)
}

// SignK8sBody signs the raw bytes of the payload's `body` field and attaches the
// additive relay_* envelope (relay_signature, relay_signed_at, relay_nonce,
// relay_key_id). It is the native-k8s-agent counterpart to Sign.
//
// Unlike Sign — which extracts named top-level fields (action/datasource_id/
// params) for proxy datasource actions — the k8s request envelope nests
// everything under `body`, so we sign the ENTIRE `body` substring. This binds
// account_id + action_name + action_params + timestamp together, and the agent
// verifies the identical bytes it executes (no field-substitution gap, no
// canonicalisation needed; transport is byte-transparent).
//
// It deliberately writes NEW top-level fields and never touches `signature`/
// `partial_auth_*` (the agent's HMAC/RSA fields), so old agents ignore it and
// reads never regress. Call this AFTER all `body` mutations so the signed bytes
// equal the forwarded bytes. sjson inserts the new keys without reformatting the
// untouched `body`, preserving the signed byte string (asserted in tests).
func (s *Signer) SignK8sBody(payload []byte) ([]byte, error) {
	body := gjson.GetBytes(payload, "body")
	if !body.Exists() {
		return nil, fmt.Errorf("sign k8s: payload has no body field")
	}
	// Prefer a zero-copy slice of the original payload (gjson populates Index
	// with the byte offset of the raw value). Index is 0 when gjson can't locate
	// it; fall back to body.Raw bytes in that case. Either way the SAME bytes are
	// signed — the agent re-extracts and verifies the same `body` substring.
	bodyBytes := []byte(body.Raw)
	if body.Index > 0 && body.Index+len(body.Raw) <= len(payload) {
		bodyBytes = payload[body.Index : body.Index+len(body.Raw)]
	}
	sig := ed25519.Sign(s.privateKey, bodyBytes)

	out := payload
	var err error
	if out, err = sjson.SetBytes(out, "relay_signature", base64.StdEncoding.EncodeToString(sig)); err != nil {
		return nil, fmt.Errorf("sign k8s: set relay_signature: %w", err)
	}
	if out, err = sjson.SetBytes(out, "relay_signed_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return nil, fmt.Errorf("sign k8s: set relay_signed_at: %w", err)
	}
	if out, err = sjson.SetBytes(out, "relay_nonce", uuid.NewString()); err != nil {
		return nil, fmt.Errorf("sign k8s: set relay_nonce: %w", err)
	}
	if s.keyID != "" {
		if out, err = sjson.SetBytes(out, "relay_key_id", s.keyID); err != nil {
			return nil, fmt.Errorf("sign k8s: set relay_key_id: %w", err)
		}
	}
	return out, nil
}
