package workflow

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	commonv1 "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// encryptedEncoding tags payloads that this codec has encrypted. Payloads
// without this encoding are passed through untouched on Decode, so workflow
// history written before encryption was enabled remains readable.
const encryptedEncoding = "binary/encrypted"

// metadataKeyID names the payload metadata entry that records which key sealed
// the payload, enabling key rotation. Payloads written before rotation support
// (or by a legacy single-key config) carry no such entry and resolve to the
// legacy key registered under the empty-string id.
const metadataKeyID = "key-id"

// legacyKeyID is the map slot for the single, unversioned key. Payloads with no
// key-id metadata decrypt against this slot, and new writes omit the key-id
// header when it is primary so their on-the-wire format stays identical to the
// pre-rotation codec.
const legacyKeyID = ""

// encryptionCodec encrypts every Temporal payload with AES-GCM before it
// leaves the process and decrypts it on the way back in. This keeps resolved
// secret values (and all other payload data) out of Temporal's persisted
// event history, the Temporal Web UI, and tctl output.
//
// It holds every key that may still appear in history (keyed by id) and one
// primary id used for new writes, so a key can be rotated by promoting a new
// primary while old keys keep decrypting existing executions — no re-encryption
// or migration needed. Retire an old key only once its executions have aged out
// of retention.
type encryptionCodec struct {
	keys      map[string]cipher.AEAD
	primaryID string
}

// NewEncryptionCodec creates a codec that encrypts payloads with AES-GCM.
//
// keys maps a key id to a raw AES key (16, 24, or 32 bytes). The legacy,
// unversioned key — the same one used by common.Encrypt — is registered under
// the empty-string id so payloads written without a key-id header still
// decrypt. primaryID selects which key seals new writes and must be present in
// keys. When primaryID is the legacy (empty) id, encoded payloads carry no
// key-id header, matching the pre-rotation wire format.
func NewEncryptionCodec(keys map[string][]byte, primaryID string) (converter.PayloadCodec, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("encryption codec: no keys provided")
	}
	if _, ok := keys[primaryID]; !ok {
		return nil, fmt.Errorf("encryption codec: primary key id %q not present in keys", primaryID)
	}

	aeads := make(map[string]cipher.AEAD, len(keys))
	for id, key := range keys {
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("encryption codec: invalid key %q: %w", id, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("encryption codec: gcm init for key %q: %w", id, err)
		}
		aeads[id] = aead
	}
	return &encryptionCodec{keys: aeads, primaryID: primaryID}, nil
}

func (c *encryptionCodec) Encode(payloads []*commonv1.Payload) ([]*commonv1.Payload, error) {
	aead := c.keys[c.primaryID]
	result := make([]*commonv1.Payload, len(payloads))
	for i, p := range payloads {
		// Marshal the whole payload (metadata + data) so the inner encoding is
		// fully restored on decode and is itself hidden from Temporal tooling.
		plaintext, err := p.Marshal()
		if err != nil {
			return nil, fmt.Errorf("encryption codec: marshal payload: %w", err)
		}

		nonce := make([]byte, aead.NonceSize())
		if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
			return nil, fmt.Errorf("encryption codec: nonce: %w", err)
		}
		// Seal prepends the nonce and appends the GCM auth tag.
		ciphertext := aead.Seal(nonce, nonce, plaintext, nil)

		metadata := map[string][]byte{
			converter.MetadataEncoding: []byte(encryptedEncoding),
		}
		// Stamp the key id so Decode can pick the right key. Omitted for the
		// legacy key to keep the wire format identical to the pre-rotation codec.
		if c.primaryID != legacyKeyID {
			metadata[metadataKeyID] = []byte(c.primaryID)
		}
		result[i] = &commonv1.Payload{Metadata: metadata, Data: ciphertext}
	}
	return result, nil
}

func (c *encryptionCodec) Decode(payloads []*commonv1.Payload) ([]*commonv1.Payload, error) {
	result := make([]*commonv1.Payload, len(payloads))
	for i, p := range payloads {
		// Pass through anything this codec didn't encrypt (legacy history).
		if string(p.GetMetadata()[converter.MetadataEncoding]) != encryptedEncoding {
			result[i] = p
			continue
		}

		// Absent key-id resolves to the legacy key.
		keyID := string(p.GetMetadata()[metadataKeyID])
		aead, ok := c.keys[keyID]
		if !ok {
			return nil, fmt.Errorf("encryption codec: no key for id %q", keyID)
		}

		ns := aead.NonceSize()
		data := p.GetData()
		if len(data) < ns {
			return nil, fmt.Errorf("encryption codec: ciphertext shorter than nonce")
		}
		nonce, ciphertext := data[:ns], data[ns:]

		plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return nil, fmt.Errorf("encryption codec: decrypt with key %q: %w", keyID, err)
		}

		var restored commonv1.Payload
		if err := restored.Unmarshal(plaintext); err != nil {
			return nil, fmt.Errorf("encryption codec: unmarshal payload: %w", err)
		}
		result[i] = &restored
	}
	return result, nil
}
