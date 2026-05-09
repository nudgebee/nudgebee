package network

import (
	"encoding/binary"
	"fmt"
	"net"
	"nudgebee/runbook/internal/tasks/types"
	"time"
)

// NtpTask implements the Task interface for checking time drift against an NTP server.
type NtpTask struct{}

func (t *NtpTask) GetName() string {
	return "network.ntp"
}

func (t *NtpTask) GetDescription() string {
	return "Check if a server's clock is in sync with an NTP time server."
}

func (t *NtpTask) GetDisplayName() string {
	return "NTP Check"
}

func (t *NtpTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	host, ok := params["host"].(string)
	if !ok || host == "" {
		// Default to pool.ntp.org if not provided?
		// Better to require it or default to a generic pool
		host = "pool.ntp.org"
	}

	port := "123"
	if p, ok := params["port"].(string); ok && p != "" {
		port = p
	}

	timeout := 5 * time.Second
	if tVal, ok := params["timeout"].(string); ok && tVal != "" {
		if d, err := time.ParseDuration(tVal); err == nil {
			timeout = d
		}
	}
	// Cap timeout
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}

	// SNTP Packet structure (48 bytes)
	// Li (2), Vn (3), Mode (3)
	req := make([]byte, 48)
	req[0] = 0x1B // Li=0, Vn=3, Mode=3 (Client)

	address := net.JoinHostPort(host, port)

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(taskCtx.GetContext(), "udp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ntp server: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			taskCtx.GetLogger().Warn("Failed to close NTP connection", "error", err)
		}
	}()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		taskCtx.GetLogger().Warn("Failed to set NTP deadline", "error", err)
	}

	// Send request
	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("failed to send ntp request: %w", err)
	}

	// Read response
	resp := make([]byte, 48)
	if _, err := conn.Read(resp); err != nil {
		return nil, fmt.Errorf("failed to read ntp response: %w", err)
	}

	// Parse time from response
	// Transmit Timestamp is at offset 40 (8 bytes)
	// Seconds (32 bits) + Fraction (32 bits)
	// NTP Epoch is 1900-01-01

	secs := binary.BigEndian.Uint32(resp[40:44])
	frac := binary.BigEndian.Uint32(resp[44:48])

	// Convert to Go time
	// NTP epoch (1900) to Unix epoch (1970) delta is 2,208,988,800 seconds
	const ntpEpochDelta = 2208988800

	ntpSeconds := float64(secs) - ntpEpochDelta
	ntpFracSeconds := float64(frac) / 4294967296.0

	ntpTime := time.Unix(int64(ntpSeconds), int64(ntpFracSeconds*1e9))

	// Calculate drift (offset)
	// Simple calculation: Local time when we parsed - NTP time
	// More accurate would be (Recv - Orig + Trans - Dest) / 2, but for "drift check"
	// comparing against local wall clock is usually the intent.

	now := time.Now()
	drift := now.Sub(ntpTime)

	return map[string]any{
		"server":        host,
		"ntp_time":      ntpTime.Format(time.RFC3339Nano),
		"local_time":    now.Format(time.RFC3339Nano),
		"drift_seconds": drift.Seconds(),
		"drift_human":   drift.String(),
	}, nil
}

func (t *NtpTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"host": {
				Type:        "string",
				Description: "NTP server hostname (default 'pool.ntp.org').",
				Required:    false,
				Default:     "pool.ntp.org",
			},
			"port": {
				Type:        "string",
				Description: "NTP port (default '123').",
				Required:    false,
				Default:     "123",
			},
			"timeout": {
				Type:        "string",
				Description: "Timeout duration (default '5s').",
				Required:    false,
				Default:     "5s",
			},
		},
	}
}

func (t *NtpTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"server": {
				Type:        "string",
				Description: "The NTP server used.",
				Required:    true,
			},
			"drift_seconds": {
				Type:        "number",
				Description: "Time difference in seconds (Local - NTP).",
				Required:    true,
			},
		},
	}
}
