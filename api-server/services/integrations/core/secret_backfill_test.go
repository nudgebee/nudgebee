package core

import (
	"context"
	"database/sql/driver"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/config"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifySecretRow(t *testing.T) {
	orig := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	t.Cleanup(func() { config.Config.NudgebeeEncryptionKey = orig })

	ciphertext, err := common.Encrypt("s3cret")
	require.NoError(t, err)

	config.Config.NudgebeeEncryptionKey = "1f1e1d1c1b1a191817161514131211100f0e0d0c0b0a09080706050403020100"
	otherKey, err := common.Encrypt("s3cret")
	require.NoError(t, err)
	config.Config.NudgebeeEncryptionKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

	for _, tc := range []struct {
		name        string
		value       string
		isEncrypted bool
		want        secretRowAction
	}{
		{"empty", "", false, secretRowKeep},
		{"ciphertext, flagged", ciphertext, true, secretRowKeep},
		{"ciphertext under a false flag gets the flag", ciphertext, false, secretRowFlag},
		{"plaintext under a false flag is encrypted", "ghp_abc123", false, secretRowEncrypt},
		{"plaintext under a true flag is encrypted", "abc", true, secretRowEncrypt},
		{"short hex plaintext is encrypted", "0123456789abcdef0123456789abcdef", false, secretRowEncrypt},
		{"ciphertext under another key is left alone", otherKey, true, secretRowSkip},
		{"ciphertext-shaped hex secret is left alone", strings.Repeat("ab", 36), false, secretRowSkip},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifySecretRow(tc.value, tc.isEncrypted, common.Decrypt))
		})
	}
}

func TestCiphertextShapeMatchesEncryptOutput(t *testing.T) {
	orig := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	t.Cleanup(func() { config.Config.NudgebeeEncryptionKey = orig })

	for _, plain := range []string{"", "x", strings.Repeat("y", 500)} {
		encrypted, err := common.Encrypt(plain)
		require.NoError(t, err)
		assert.True(t, ciphertextShape.MatchString(encrypted), "Encrypt(%d chars) must look like ciphertext", len(plain))
	}
	assert.False(t, ciphertextShape.MatchString(strings.Repeat("a", 55)), "odd length is not hex bytes")
	assert.False(t, ciphertextShape.MatchString(strings.Repeat("AB", 30)), "Encrypt emits lowercase hex")
}

type backfillStub struct{ stubIntegration }

func (backfillStub) ConfigSchema() IntegrationSchema {
	return IntegrationSchema{Type: ToolSchemaTypeObject, Properties: map[string]IntegrationSchemaProperty{
		"url":      {Type: ToolSchemaTypeString},
		"password": {Type: ToolSchemaTypeString, IsEncrypted: true},
	}}
}

func init() { RegisterIntegration(backfillStub{stubIntegration{name: "backfill_stub"}}) }

func TestBackfillSecretEncryption(t *testing.T) {
	withTestEncryptionKey(t)
	ciphertext := mustEncrypt(t, "kept")
	foreign := strings.Repeat("ab", 40)

	pkgMock.ExpectQuery(`(?s)SELECT icv\.id::text, i\.type, .* FROM integration_config_values icv .*icv\.name = ANY\(\$1\) OR i\.type = 'llm'`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "type", "source", "name", "value", "is_encrypted"}).
			AddRow("r1", "backfill_stub", "user", "password", "plain-1", false).
			AddRow("r2", "backfill_stub", "user", "password", ciphertext, false).
			AddRow("r3", "backfill_stub", "user", "url", "https://x.example", false).
			AddRow("r4", "llm", "user", "llm_provider_api_key_summary_agent", "sk-abc", true).
			AddRow("r5", "backfill_stub", "user", "password", foreign, true).
			AddRow("r6", "backfill_stub", "user", "password", "edited-meanwhile", false))

	encryptUpdate := regexp.QuoteMeta(`UPDATE integration_config_values SET value = $1, is_encrypted = true`)
	flagUpdate := regexp.QuoteMeta(`UPDATE integration_config_values SET is_encrypted = true`)
	pkgMock.ExpectExec(encryptUpdate).WithArgs(decryptsTo{"plain-1"}, "r1", "plain-1").WillReturnResult(sqlmock.NewResult(0, 1))
	pkgMock.ExpectExec(flagUpdate).WithArgs("r2", ciphertext).WillReturnResult(sqlmock.NewResult(0, 1))
	pkgMock.ExpectExec(encryptUpdate).WithArgs(decryptsTo{"sk-abc"}, "r4", "sk-abc").WillReturnResult(sqlmock.NewResult(0, 1))
	// A save landed between the read and the update: the guarded UPDATE matches nothing.
	pkgMock.ExpectExec(encryptUpdate).WithArgs(decryptsTo{"edited-meanwhile"}, "r6", "edited-meanwhile").WillReturnResult(sqlmock.NewResult(0, 0))

	got, err := BackfillSecretEncryption(context.Background(), slog.New(slog.DiscardHandler))

	require.NoError(t, err)
	assert.Equal(t, SecretBackfillResult{Encrypted: 2, Flagged: 1, Skipped: 1}, got)
	assert.NoError(t, pkgMock.ExpectationsWereMet())
}

// decryptsTo matches an argument that is ciphertext of the given plaintext.
type decryptsTo struct{ plaintext string }

func (d decryptsTo) Match(v driver.Value) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	plain, err := common.Decrypt(s)
	return err == nil && plain == d.plaintext
}

func TestBackfillSecretEncryptionStopsAtShutdown(t *testing.T) {
	withTestEncryptionKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pkgMock.ExpectQuery(`(?s)SELECT icv\.id::text, i\.type, .* FROM integration_config_values icv .*icv\.name = ANY\(\$1\) OR i\.type = 'llm'`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "type", "source", "name", "value", "is_encrypted"}).
			AddRow("s1", "backfill_stub", "user", "password", "plain-1", false).
			AddRow("s2", "backfill_stub", "user", "password", "plain-2", false))
	// Shutdown arrives while the first row is being written; the second row
	// must not be attempted (an unexpected Exec would fail the mock).
	pkgMock.ExpectExec(regexp.QuoteMeta(`UPDATE integration_config_values SET value = $1, is_encrypted = true`)).
		WithArgs(cancelOnMatch{decryptsTo{"plain-1"}, cancel}, "s1", "plain-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	got, err := BackfillSecretEncryption(ctx, slog.New(slog.DiscardHandler))

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, got.Encrypted)
	assert.Equal(t, 1, got.Failed, "only the in-flight row is counted; the rest are not attempted")
	assert.NoError(t, pkgMock.ExpectationsWereMet())
}

// cancelOnMatch cancels a context when the wrapped argument matches.
type cancelOnMatch struct {
	decryptsTo
	cancel context.CancelFunc
}

func (c cancelOnMatch) Match(v driver.Value) bool {
	if !c.decryptsTo.Match(v) {
		return false
	}
	c.cancel()
	return true
}
