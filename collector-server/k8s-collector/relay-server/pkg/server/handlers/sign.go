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

// signK8sPayload signs the raw `body` bytes for native k8s agents (additive
// relay_* fields). Returns the payload unchanged if signer is nil or signing
// fails — failing open here means an unsigned mutation, which the agent then
// rejects (401), rather than dropping the request.
func signK8sPayload(payload []byte, signer *signing.Signer, logger *slog.Logger) []byte {
	if signer == nil {
		return payload
	}
	signed, err := signer.SignK8sBody(payload)
	if err != nil {
		logger.Error("failed to sign k8s body, sending unsigned", "err", err)
		return payload
	}
	return signed
}
