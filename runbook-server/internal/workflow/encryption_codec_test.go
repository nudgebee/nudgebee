package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonv1 "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// 32-byte AES-256 keys.
var (
	testKey  = []byte("0123456789abcdef0123456789abcdef")
	testKey2 = []byte("fedcba9876543210fedcba9876543210")
)

func newPayload(data string) *commonv1.Payload {
	return &commonv1.Payload{
		Metadata: map[string][]byte{
			converter.MetadataEncoding: []byte("json/plain"),
		},
		Data: []byte(data),
	}
}

// legacyCodec builds a single-key codec under the legacy (empty) id, matching
// the pre-rotation configuration.
func legacyCodec(t *testing.T, key []byte) converter.PayloadCodec {
	t.Helper()
	c, err := NewEncryptionCodec(map[string][]byte{"": key}, "")
	require.NoError(t, err)
	return c
}

func TestNewEncryptionCodec_InvalidKey(t *testing.T) {
	_, err := NewEncryptionCodec(map[string][]byte{"": []byte("too-short")}, "")
	assert.Error(t, err)
}

func TestNewEncryptionCodec_NoKeys(t *testing.T) {
	_, err := NewEncryptionCodec(map[string][]byte{}, "")
	assert.Error(t, err)
}

func TestNewEncryptionCodec_PrimaryNotInKeys(t *testing.T) {
	_, err := NewEncryptionCodec(map[string][]byte{"v1": testKey}, "v2")
	assert.Error(t, err)
}

func TestEncryptionCodec_RoundTrip(t *testing.T) {
	codec := legacyCodec(t, testKey)

	original := []*commonv1.Payload{
		newPayload(`{"token":"super-secret-value"}`),
		newPayload(`{"url":"https://api.example.com"}`),
	}

	encoded, err := codec.Encode(original)
	require.NoError(t, err)
	require.Len(t, encoded, 2)

	for _, p := range encoded {
		// Ciphertext must be tagged and must not contain the plaintext.
		assert.Equal(t, encryptedEncoding, string(p.GetMetadata()[converter.MetadataEncoding]))
		assert.NotContains(t, string(p.GetData()), "super-secret-value")
		assert.NotContains(t, string(p.GetData()), "api.example.com")
		// Legacy primary omits the key-id header (wire format unchanged).
		_, hasKeyID := p.GetMetadata()[metadataKeyID]
		assert.False(t, hasKeyID)
	}

	decoded, err := codec.Decode(encoded)
	require.NoError(t, err)
	require.Len(t, decoded, 2)

	assert.Equal(t, original[0].GetData(), decoded[0].GetData())
	assert.Equal(t, original[1].GetData(), decoded[1].GetData())
	// Inner encoding metadata is restored.
	assert.Equal(t, "json/plain", string(decoded[0].GetMetadata()[converter.MetadataEncoding]))
}

func TestEncryptionCodec_NonceIsRandom(t *testing.T) {
	codec := legacyCodec(t, testKey)

	in := []*commonv1.Payload{newPayload(`{"a":"b"}`)}
	first, err := codec.Encode(in)
	require.NoError(t, err)
	second, err := codec.Encode(in)
	require.NoError(t, err)

	// Same plaintext, different ciphertext (random nonce per Encode).
	assert.NotEqual(t, first[0].GetData(), second[0].GetData())
}

func TestEncryptionCodec_Decode_PassesThroughUnencrypted(t *testing.T) {
	codec := legacyCodec(t, testKey)

	// Legacy history written before encryption was enabled.
	legacy := []*commonv1.Payload{newPayload(`{"plain":"data"}`)}
	decoded, err := codec.Decode(legacy)
	require.NoError(t, err)
	assert.Equal(t, legacy[0].GetData(), decoded[0].GetData())
	assert.Equal(t, "json/plain", string(decoded[0].GetMetadata()[converter.MetadataEncoding]))
}

func TestEncryptionCodec_Decode_TamperedFails(t *testing.T) {
	codec := legacyCodec(t, testKey)

	encoded, err := codec.Encode([]*commonv1.Payload{newPayload(`{"a":"b"}`)})
	require.NoError(t, err)
	// Flip a byte in the ciphertext body; GCM auth must reject it.
	encoded[0].Data[len(encoded[0].Data)-1] ^= 0xFF

	_, err = codec.Decode(encoded)
	assert.Error(t, err)
}

func TestEncryptionCodec_WrongKeyFails(t *testing.T) {
	enc := legacyCodec(t, testKey)
	encoded, err := enc.Encode([]*commonv1.Payload{newPayload(`{"a":"b"}`)})
	require.NoError(t, err)

	dec := legacyCodec(t, testKey2)
	_, err = dec.Decode(encoded)
	assert.Error(t, err)
}

// Rotation: a codec holding both keys, with v2 primary, seals new writes with
// v2 (stamping the key-id) while still decrypting v1-sealed and legacy
// (unstamped) history.
func TestEncryptionCodec_Rotation(t *testing.T) {
	// Old deployment: legacy key is primary.
	oldCodec := legacyCodec(t, testKey)
	legacySealed, err := oldCodec.Encode([]*commonv1.Payload{newPayload(`{"old":"data"}`)})
	require.NoError(t, err)

	// New deployment: keep legacy key for reads, add v2 as the new primary.
	rotated, err := NewEncryptionCodec(map[string][]byte{
		"":   testKey,  // legacy, still needed to read old history
		"v2": testKey2, // new primary
	}, "v2")
	require.NoError(t, err)

	// New writes are stamped with the v2 key-id.
	fresh, err := rotated.Encode([]*commonv1.Payload{newPayload(`{"new":"data"}`)})
	require.NoError(t, err)
	assert.Equal(t, "v2", string(fresh[0].GetMetadata()[metadataKeyID]))

	// The rotated codec reads both new (v2) and old (legacy, unstamped) history.
	decFresh, err := rotated.Decode(fresh)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"new":"data"}`), decFresh[0].GetData())

	decLegacy, err := rotated.Decode(legacySealed)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"old":"data"}`), decLegacy[0].GetData())
}

// A codec missing the key id stamped on a payload must fail closed.
func TestEncryptionCodec_UnknownKeyID(t *testing.T) {
	writer, err := NewEncryptionCodec(map[string][]byte{"v2": testKey2}, "v2")
	require.NoError(t, err)
	sealed, err := writer.Encode([]*commonv1.Payload{newPayload(`{"a":"b"}`)})
	require.NoError(t, err)

	// A reader that only knows the legacy key cannot resolve "v2".
	reader := legacyCodec(t, testKey)
	_, err = reader.Decode(sealed)
	assert.Error(t, err)
}

// Chained with compression exactly as wired in main.go: encryption listed
// first, compression second. CodecDataConverter encodes in reverse order, so
// the effective pipeline is compress-then-encrypt / decrypt-then-decompress.
func TestEncryptionCodec_ChainedWithCompression(t *testing.T) {
	enc := legacyCodec(t, testKey)
	dc := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		enc,
		NewCompressionCodec(16),
	)

	type payload struct {
		Token string `json:"token"`
		Blob  string `json:"blob"`
	}
	original := payload{Token: "super-secret-value", Blob: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}

	p, err := dc.ToPayload(original)
	require.NoError(t, err)
	// On the wire the payload is encrypted (outermost codec on encode).
	assert.Equal(t, encryptedEncoding, string(p.GetMetadata()[converter.MetadataEncoding]))
	assert.NotContains(t, string(p.GetData()), "super-secret-value")

	var out payload
	require.NoError(t, dc.FromPayload(p, &out))
	assert.Equal(t, original, out)
}
