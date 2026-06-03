package handlers

import (
	"log/slog"

	"nudgebee/relay-server/pkg/signing"
)

// signPayload signs the payload if a signer is configured.
// Returns the original payload unchanged if signer is nil or signing fails.
func signPayload(payload []byte, signer *signing.Signer, logger *slog.Logger) []byte {
	if signer == nil {
		return payload
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		logger.Error("failed to sign message, sending unsigned", "err", err)
		return payload
	}
	return signed
}
