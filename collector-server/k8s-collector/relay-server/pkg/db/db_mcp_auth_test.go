package db

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const testEncryptionKey = "000102030405060708090a0b0c0d0e0f000102030405060708090a0b0c0d0e0f"

// encryptForTest produces the hex(nonce||ciphertext) AES-GCM format
// utils.Decrypt reads.
func encryptForTest(t *testing.T, plain string) string {
	t.Helper()
	key, _ := hex.DecodeString(testEncryptionKey)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	return hex.EncodeToString(append(nonce, gcm.Seal(nil, nonce, []byte(plain), nil)...))
}

func TestGetMCPIntegrationAuth_DecryptsAndMapsCredentials(t *testing.T) {
	s, mock := newMockStore(t)
	s.encryptionKey = testEncryptionKey
	mock.ExpectQuery(`FROM integrations i`).
		WithArgs("int-1", "acct-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "value", "is_encrypted"}).
			AddRow("url", "https://mcp.example.com/mcp", false).
			AddRow("auth_type", "bearer", false).
			AddRow("bearer_token", encryptForTest(t, "secret-token"), true).
			AddRow("llm_instructions", "ignored", false))

	auth, err := s.GetMCPIntegrationAuth(context.Background(), "acct-1", "int-1")
	if err != nil {
		t.Fatalf("GetMCPIntegrationAuth: %v", err)
	}
	if auth.URL != "https://mcp.example.com/mcp" || auth.AuthType != "bearer" {
		t.Fatalf("unexpected endpoint/auth type: %+v", auth)
	}
	if auth.AuthCredentials["bearer_token"] != "secret-token" || len(auth.AuthCredentials) != 1 {
		t.Fatalf("unexpected credentials: %+v", auth.AuthCredentials)
	}
}

func TestGetMCPIntegrationAuth_NotFoundForOtherAccount(t *testing.T) {
	s, mock := newMockStore(t)
	mock.ExpectQuery(`FROM integrations i`).
		WithArgs("int-1", "other-acct").
		WillReturnRows(sqlmock.NewRows([]string{"name", "value", "is_encrypted"}))

	_, err := s.GetMCPIntegrationAuth(context.Background(), "other-acct", "int-1")
	if !errors.Is(err, ErrMCPIntegrationNotFound) {
		t.Fatalf("expected ErrMCPIntegrationNotFound, got %v", err)
	}
}

// A value that fails to decrypt must fail the load, so a DELETE never goes out
// with missing credentials.
func TestGetMCPIntegrationAuth_DecryptFailureIsAnError(t *testing.T) {
	s, mock := newMockStore(t)
	s.encryptionKey = testEncryptionKey
	mock.ExpectQuery(`FROM integrations i`).
		WithArgs("int-1", "acct-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "value", "is_encrypted"}).
			AddRow("url", "https://mcp.example.com/mcp", false).
			AddRow("bearer_token", "not-hex", true))

	if _, err := s.GetMCPIntegrationAuth(context.Background(), "acct-1", "int-1"); err == nil {
		t.Fatal("expected a decrypt error")
	}
}

func TestGetMCPIntegrationAuth_EncryptedValueWithoutKeyIsAnError(t *testing.T) {
	s, mock := newMockStore(t) // no encryption key
	mock.ExpectQuery(`FROM integrations i`).
		WithArgs("int-1", "acct-1").
		WillReturnRows(sqlmock.NewRows([]string{"name", "value", "is_encrypted"}).
			AddRow("bearer_token", "abcd", true))

	if _, err := s.GetMCPIntegrationAuth(context.Background(), "acct-1", "int-1"); err == nil {
		t.Fatal("expected an error when an encrypted value can't be decrypted")
	}
}
