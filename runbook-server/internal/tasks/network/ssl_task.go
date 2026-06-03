package network

import (
	"crypto/tls"
	"fmt"
	"net"
	"nudgebee/runbook/internal/tasks/types"
	"time"
)

// SslTask implements the Task interface for checking SSL/TLS certificates.
type SslTask struct{}

func (t *SslTask) GetName() string {
	return "network.ssl"
}

func (t *SslTask) GetDescription() string {
	return "Check SSL/TLS certificate validity and expiration for a host."
}

func (t *SslTask) GetDisplayName() string {
	return "SSL Check"
}

func (t *SslTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	host, ok := params["host"].(string)
	if !ok || host == "" {
		return nil, fmt.Errorf("host parameter is required")
	}

	port := "443"
	if p, ok := params["port"]; ok {
		port = fmt.Sprintf("%v", p)
	}

	address := net.JoinHostPort(host, port)
	timeout := 10 * time.Second

	// Connect to the host
	dialer := &net.Dialer{Timeout: timeout}

	// 1. Dial TCP with Context
	rawConn, err := dialer.DialContext(taskCtx.GetContext(), "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", address, err)
	}
	defer func() {
		if err := rawConn.Close(); err != nil {
			taskCtx.GetLogger().Warn("Failed to close raw TCP connection", "error", err)
		}
	}()

	// 2. Upgrade to TLS
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // We want to inspect the cert even if it's invalid (e.g. expired)
		ServerName:         host, // Important for SNI
	}
	conn := tls.Client(rawConn, tlsConfig)

	// 3. Handshake with Context (requires Go 1.17+)
	// If HandshakeContext is not available (older Go), standard Handshake() uses the underlying conn deadline if set.
	// We can set deadline on rawConn to be safe.
	if err := rawConn.SetDeadline(time.Now().Add(timeout)); err != nil {
		taskCtx.GetLogger().Warn("Failed to set raw TCP connection deadline", "error", err)
	}

	if err := conn.HandshakeContext(taskCtx.GetContext()); err != nil {
		return nil, fmt.Errorf("tls handshake failed: %w", err)
	}

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no certificates found for %s", address)
	}

	cert := state.PeerCertificates[0]

	// Calculate basic validity
	now := time.Now()
	isValid := now.After(cert.NotBefore) && now.Before(cert.NotAfter)

	return map[string]any{
		"subject":    cert.Subject.CommonName,
		"issuer":     cert.Issuer.CommonName,
		"not_before": cert.NotBefore.Format(time.RFC3339),
		"not_after":  cert.NotAfter.Format(time.RFC3339),
		"dns_names":  cert.DNSNames,
		"valid":      isValid, // Basic time check
		// "signature_algo": cert.SignatureAlgorithm.String(),
	}, nil
}

func (t *SslTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"host": {
				Type:        "string",
				Description: "The hostname to check.",
				Required:    true,
			},
			"port": {
				Type:        "string",
				Description: "The port (default 443).",
				Required:    false,
				Default:     "443",
			},
		},
	}
}

func (t *SslTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"subject": {
				Type:        "string",
				Description: "Common Name of the subject.",
				Required:    true,
			},
			"issuer": {
				Type:        "string",
				Description: "Common Name of the issuer.",
				Required:    true,
			},
			"not_after": {
				Type:        "string",
				Description: "Expiration date (RFC3339).",
				Required:    true,
			},
			"valid": {
				Type:        "boolean",
				Description: "True if current time is within validity period.",
				Required:    true,
			},
		},
	}
}
