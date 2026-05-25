package core

import (
	"errors"
	"strings"
)

const (
	TimeoutErrorMessage      = "connection timed out. Please verify your secrets and try again"
	AgentNotConnectedMessage = "the kubernetes agent is not connected. Please ensure the agent is running and connected to the relay server"
	AgentNotFoundMessage     = "no agent found for this account. Please deploy the kubernetes agent first"
	UnauthorizedMessage      = "unauthorized access to relay server. Please check your relay server credentials"
	GenericConnectionMessage = "failed to connect to the cluster. Please verify the agent is running and your configuration is correct"
)

func IsTimeoutError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func IsAgentNotConnectedError(err error) bool {
	errLower := strings.ToLower(err.Error())
	return strings.Contains(errLower, "agent not connected") || strings.Contains(errLower, "agent not found")
}

func IsUnauthorizedError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unauthorized")
}

// HandleRelayError transforms relay errors into user-friendly messages
func HandleRelayError(err error) []error {
	if err == nil {
		return []error{}
	}

	errLower := strings.ToLower(err.Error())

	switch {
	case strings.Contains(errLower, "timeout"):
		return []error{errors.New(TimeoutErrorMessage)}
	case strings.Contains(errLower, "agent not connected"):
		return []error{errors.New(AgentNotConnectedMessage)}
	case strings.Contains(errLower, "agent not found") || strings.Contains(errLower, "after max retries"):
		return []error{errors.New(AgentNotFoundMessage)}
	case strings.Contains(errLower, "unauthorized"):
		return []error{errors.New(UnauthorizedMessage)}
	case strings.Contains(errLower, "relay"):
		// Generic relay error - make it more user-friendly
		return []error{errors.New(GenericConnectionMessage)}
	default:
		return []error{err}
	}
}

// HandleRelayTimeoutError is kept for backward compatibility
// Deprecated: Use HandleRelayError instead
func HandleRelayTimeoutError(err error) []error {
	return HandleRelayError(err)
}
