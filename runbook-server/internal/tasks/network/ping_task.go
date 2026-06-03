package network

import (
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// PingTask implements the Task interface for ICMP ping.
type PingTask struct{}

func (t *PingTask) GetName() string {
	return "network.ping"
}

func (t *PingTask) GetDescription() string {
	return "Ping a host to check if it's reachable."
}

func (t *PingTask) GetDisplayName() string {
	return "Ping"
}

func (t *PingTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	host, ok := params["host"].(string)
	if !ok || host == "" {
		return nil, fmt.Errorf("host parameter is required")
	}

	// Security: Validate host input to prevent argument injection
	// Allow alphanumeric, dots, colons (IPv6), and hyphens.
	validHost := regexp.MustCompile(`^[a-zA-Z0-9.:-]+$`)
	if !validHost.MatchString(host) {
		return nil, fmt.Errorf("invalid host format: contains illegal characters")
	}
	// Prevent flag injection (starting with -)
	if strings.HasPrefix(host, "-") {
		return nil, fmt.Errorf("invalid host format: cannot start with '-'")
	}

	count := 3
	if c, ok := params["count"].(float64); ok {
		count = int(c)
	}
	// Cap count at 30 to prevent long-running pings
	if count > 30 {
		count = 30
	}
	if count < 1 {
		count = 1
	}

	// Construct command based on OS (mostly for local dev support, prod is Linux)
	var cmd *exec.Cmd
	var args []string

	if runtime.GOOS == "windows" {
		args = []string{"-n", fmt.Sprintf("%d", count), host}
		cmd = exec.CommandContext(taskCtx.GetContext(), "ping", args...)
	} else {
		// Linux/Unix
		// -c count
		args = []string{"-c", fmt.Sprintf("%d", count), host}
		cmd = exec.CommandContext(taskCtx.GetContext(), "ping", args...)
	}

	outputBytes, cmdErr := cmd.CombinedOutput()
	output := string(outputBytes)

	if cmdErr != nil {
		taskCtx.GetLogger().Debug("Ping command returned error", "error", cmdErr.Error(), "output", output)
		// We still parse output for partial success (packet loss < 100)
	}

	var reachable bool // Declare, initialize later

	// Parse stats (Packet Loss and Avg Latency)
	packetLoss := 100.0
	avgLatency := 0.0

	// Regex for packet loss: "X% packet loss"
	lossRe := regexp.MustCompile(`(\d+)%\s+packet\s+loss`)
	lossMatch := lossRe.FindStringSubmatch(output)
	if len(lossMatch) > 1 {
		if p, err := strconv.ParseFloat(lossMatch[1], 64); err == nil {
			packetLoss = p
		}
	}

	// Regex for latency: "min/avg/max = X/Y/Z" (Linux) or "min/avg/max/stddev = X/Y/Z/W" (Mac)
	// Mac: round-trip min/avg/max/stddev = 25.064/26.686/29.563/1.626 ms
	// Linux (Busybox): round-trip min/avg/max = 2.3/3.4/5.6 ms
	// Linux (iputils): rtt min/avg/max/mdev = 2.3/3.4/5.6/0.1 ms
	latRe := regexp.MustCompile(`(min/avg/max|round-trip).*?=\s*[\d\.]+/([\d\.]+)/`)
	latMatch := latRe.FindStringSubmatch(output)
	if len(latMatch) > 1 {
		if l, err := strconv.ParseFloat(latMatch[2], 64); err == nil {
			avgLatency = l
		}
	}

	// If the command failed but we parsed some packet loss < 100%, it might be partial success?
	// Usually ping returns non-zero if any packet is lost or host unreachable.
	// We'll rely on the 'reachable' boolean as strict success (0% loss usually required for exit 0 on some pings, or just 'some' reply).
	// Actually, ping exit code 0 means "at least one response" on many systems, or "all responses" on others.
	// Let's rely on packetLoss for strictness.

	if packetLoss < 100 {
		reachable = true // At least some packets got through
	} else {
		reachable = false
	}

	return map[string]any{
		"host":        host,
		"reachable":   reachable,
		"packet_loss": packetLoss,
		"avg_latency": avgLatency,
		"output":      output, // Debug info
	}, nil
}

func (t *PingTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"host": {
				Type:        "string",
				Description: "Hostname or IP to ping.",
				Required:    true,
			},
			"count": {
				Type:        "number",
				Description: "Number of packets to send (default 3).",
				Required:    false,
			},
		},
	}
}

func (t *PingTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"reachable": {
				Type:        "boolean",
				Description: "True if any packets were received.",
				Required:    true,
			},
			"packet_loss": {
				Type:        "number",
				Description: "Percentage of packets lost.",
				Required:    true,
			},
			"avg_latency": {
				Type:        "number",
				Description: "Average round-trip time in milliseconds.",
				Required:    false,
			},
		},
	}
}
