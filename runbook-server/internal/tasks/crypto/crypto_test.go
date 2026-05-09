package crypto_test

import (
	"testing"

	"nudgebee/runbook/internal/tasks/crypto"
	"nudgebee/runbook/internal/tasks/testutils"

	"github.com/stretchr/testify/assert"
)

// Simple logger mock
type NoOpLogger struct{}

func (l *NoOpLogger) Debug(msg string, keyvals ...interface{}) {}
func (l *NoOpLogger) Info(msg string, keyvals ...interface{})  {}
func (l *NoOpLogger) Warn(msg string, keyvals ...interface{})  {}
func (l *NoOpLogger) Error(msg string, keyvals ...interface{}) {}

func TestCryptoEncodeTask(t *testing.T) {
	task := &crypto.CryptoEncodeTask{}
	ctx := testutils.NewTestTaskContext("tenant", "account", "user", &NoOpLogger{})

	// Test Base64
	params := map[string]any{
		"data":      "hello world",
		"algorithm": "base64",
	}
	res, err := task.Execute(ctx, params)
	assert.NoError(t, err)
	resMap, ok := res.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "aGVsbG8gd29ybGQ=", resMap["data"])

	// Test Hex
	params = map[string]any{
		"data":      "hello world",
		"algorithm": "hex",
	}
	res, err = task.Execute(ctx, params)
	assert.NoError(t, err)
	resMap, ok = res.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "68656c6c6f20776f726c64", resMap["data"])
}

func TestCryptoDecodeTask(t *testing.T) {
	task := &crypto.CryptoDecodeTask{}
	ctx := testutils.NewTestTaskContext("tenant", "account", "user", &NoOpLogger{})

	// Test Base64
	params := map[string]any{
		"data":      "aGVsbG8gd29ybGQ=",
		"algorithm": "base64",
	}
	res, err := task.Execute(ctx, params)
	assert.NoError(t, err)
	resMap, ok := res.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "hello world", resMap["data"])

	// Test Hex
	params = map[string]any{
		"data":      "68656c6c6f20776f726c64",
		"algorithm": "hex",
	}
	res, err = task.Execute(ctx, params)
	assert.NoError(t, err)
	resMap, ok = res.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "hello world", resMap["data"])
}

func TestCryptoHashTask(t *testing.T) {
	task := &crypto.CryptoHashTask{}
	ctx := testutils.NewTestTaskContext("tenant", "account", "user", &NoOpLogger{})

	// Test MD5
	params := map[string]any{
		"data":      "hello world",
		"algorithm": "md5",
	}
	res, err := task.Execute(ctx, params)
	assert.NoError(t, err)
	resMap, ok := res.(map[string]any)
	assert.True(t, ok)
	// md5("hello world") = 5eb63bbbe01eeed093cb22bb8f5acdc3
	assert.Equal(t, "5eb63bbbe01eeed093cb22bb8f5acdc3", resMap["data"])
}

func TestCryptoEncryptDecryptTask(t *testing.T) {
	encryptTask := &crypto.CryptoEncryptTask{}
	decryptTask := &crypto.CryptoDecryptTask{}
	ctx := testutils.NewTestTaskContext("tenant", "account", "user", &NoOpLogger{})

	key := "mysecretkey"
	data := "sensitive data"

	// Encrypt
	encParams := map[string]any{
		"data": data,
		"key":  key,
	}
	encRes, err := encryptTask.Execute(ctx, encParams)
	assert.NoError(t, err)
	encResMap, ok := encRes.(map[string]any)
	assert.True(t, ok)
	encryptedData := encResMap["data"].(string)

	// Decrypt
	decParams := map[string]any{
		"data": encryptedData,
		"key":  key,
	}
	decRes, err := decryptTask.Execute(ctx, decParams)
	assert.NoError(t, err)
	decResMap, ok := decRes.(map[string]any)
	assert.True(t, ok)
	decryptedData := decResMap["data"].(string)

	assert.Equal(t, data, decryptedData)
}

func TestCryptoEncryptDecryptTaskWithBase64Key(t *testing.T) {
	encryptTask := &crypto.CryptoEncryptTask{}
	decryptTask := &crypto.CryptoDecryptTask{}
	ctx := testutils.NewTestTaskContext("tenant", "account", "user", &NoOpLogger{})

	// 32 bytes key encoded in Base64
	// "12345678901234567890123456789012"
	key := "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI="
	data := "sensitive data"

	// Encrypt
	encParams := map[string]any{
		"data":         data,
		"key":          key,
		"key_encoding": "base64",
		"algorithm":    "aes-256-gcm",
	}
	encRes, err := encryptTask.Execute(ctx, encParams)
	assert.NoError(t, err)
	encResMap, ok := encRes.(map[string]any)
	assert.True(t, ok)
	encryptedData := encResMap["data"].(string)

	// Decrypt
	decParams := map[string]any{
		"data":         encryptedData,
		"key":          key,
		"key_encoding": "base64",
		"algorithm":    "aes-256-gcm",
	}
	decRes, err := decryptTask.Execute(ctx, decParams)
	assert.NoError(t, err)
	decResMap, ok := decRes.(map[string]any)
	assert.True(t, ok)
	decryptedData := decResMap["data"].(string)

	assert.Equal(t, data, decryptedData)
}

func TestCryptoEncryptDecryptTaskWithHexKey(t *testing.T) {
	encryptTask := &crypto.CryptoEncryptTask{}
	decryptTask := &crypto.CryptoDecryptTask{}
	ctx := testutils.NewTestTaskContext("tenant", "account", "user", &NoOpLogger{})

	// 32 bytes key encoded in Hex
	// "12345678901234567890123456789012"
	key := "3132333435363738393031323334353637383930313233343536373839303132"
	data := "sensitive data"

	// Encrypt
	encParams := map[string]any{
		"data":         data,
		"key":          key,
		"key_encoding": "hex",
		"algorithm":    "aes-256-gcm",
	}
	encRes, err := encryptTask.Execute(ctx, encParams)
	assert.NoError(t, err)
	encResMap, ok := encRes.(map[string]any)
	assert.True(t, ok)
	encryptedData := encResMap["data"].(string)

	// Decrypt
	decParams := map[string]any{
		"data":         encryptedData,
		"key":          key,
		"key_encoding": "hex",
		"algorithm":    "aes-256-gcm",
	}
	decRes, err := decryptTask.Execute(ctx, decParams)
	assert.NoError(t, err)
	decResMap, ok := decRes.(map[string]any)
	assert.True(t, ok)
	decryptedData := decResMap["data"].(string)

	assert.Equal(t, data, decryptedData)
}

func TestCryptoEncryptDecryptTaskInvalidKeyLength(t *testing.T) {
	encryptTask := &crypto.CryptoEncryptTask{}
	ctx := testutils.NewTestTaskContext("tenant", "account", "user", &NoOpLogger{})

	// Short key (not 32 bytes)
	key := "shortkey"
	data := "sensitive data"

	// Encrypt with text encoding (should work as it hashes)
	encParams := map[string]any{
		"data":         data,
		"key":          key,
		"key_encoding": "text",
	}
	_, err := encryptTask.Execute(ctx, encParams)
	assert.NoError(t, err)

	// Encrypt with base64 encoding (should fail due to length after decode)
	// "shortkey" is not even valid base64 for 32 bytes
	// let's use a valid base64 that decodes to < 32 bytes
	// "aGVsbG8=" -> "hello" (5 bytes)
	encParams = map[string]any{
		"data":         data,
		"key":          "aGVsbG8=",
		"key_encoding": "base64",
	}
	_, err = encryptTask.Execute(ctx, encParams)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid key length")
}
