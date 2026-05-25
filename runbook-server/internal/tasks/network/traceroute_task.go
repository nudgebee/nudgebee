package network

import (
	"bufio"
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"go.temporal.io/sdk/log" // Added import
)

// TracerouteTask implements the Task interface for network path analysis.
type TracerouteTask struct{}

func (t *TracerouteTask) GetName() string {
	return "network.traceroute"
}

func (t *TracerouteTask) GetDescription() string {
	return "Trace the network path to a host to diagnose routing issues."
}

func (t *TracerouteTask) GetDisplayName() string {
	return "Traceroute"
}

func (t *TracerouteTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	host, ok := params["host"].(string)
	if !ok || host == "" {
		return nil, fmt.Errorf("host parameter is required")
	}

	// Security: Strict Input Validation
	validHost := regexp.MustCompile(`^[a-zA-Z0-9.:-]+$`)
	if !validHost.MatchString(host) {
		return nil, fmt.Errorf("invalid host format")
	}
	if strings.HasPrefix(host, "-") {
		return nil, fmt.Errorf("host cannot start with hyphen")
	}

	maxHops := 30
	if m, ok := params["max_hops"].(float64); ok {
		maxHops = int(m)
	}
	// Cap max_hops to prevent excessive runtime
	if maxHops > 60 {
		maxHops = 60
	}
	if maxHops < 1 {
		maxHops = 1
	}

	// Traceroute flags:
	// -n: Do not resolve IP addresses to their domain names (faster)
	// -m: Max time-to-live (max hops)
	// -w: Wait time for response (seconds)
	// -q: Number of queries per hop (default 3)
	args := []string{"-n", "-m", fmt.Sprintf("%d", maxHops), "-w", "2", "-q", "1", host}

	cmd := exec.CommandContext(taskCtx.GetContext(), "traceroute", args...)
	outputBytes, err := cmd.CombinedOutput()

	// Traceroute often exits with non-zero if destination is unreachable,
	// but we still want the partial output.
	// So we don't strictly return error if err != nil, unless output is empty.
	output := string(outputBytes)
	if output == "" && err != nil {
		return nil, fmt.Errorf("traceroute failed: %w", err)
	}

	// Parse the output into structured hops
	hops := parseTraceroute(taskCtx.GetLogger(), output)

	return map[string]any{
		"host":    host,
		"hops":    hops,
		"raw":     output,
		"reached": reachedDestination(hops, host), // Approximate check
	}, nil
}

type Hop struct {
	HopNumber int     `json:"hop"`
	IP        string  `json:"ip"`
	Latency   float64 `json:"latency_ms"` // -1 if timeout (*)
}

func parseTraceroute(logger log.Logger, raw string) []Hop {
	var hops []Hop
	scanner := bufio.NewScanner(strings.NewReader(raw))

	// Regex for a successful hop line: " 1  172.17.0.1  0.043 ms"
	lineRe := regexp.MustCompile(`^\s*(\d+)\s+([0-9a-fA-F:.]+)\s+([\d\.]+)\s+ms`)
	timeoutRe := regexp.MustCompile(`^\s*(\d+)\s+\*`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if matches := lineRe.FindStringSubmatch(line); len(matches) == 4 {
			hopNum, err := strconv.Atoi(matches[1])
			if err != nil {
				logger.Warn("Failed to parse hop number", "line", line, "error", err)
				continue
			}
			latency, err := strconv.ParseFloat(matches[3], 64)
			if err != nil {
				logger.Warn("Failed to parse hop latency", "line", line, "error", err)
				continue
			}
			hops = append(hops, Hop{
				HopNumber: hopNum,
				IP:        matches[2],
				Latency:   latency,
			})
			continue
		}

		if matches := timeoutRe.FindStringSubmatch(line); len(matches) == 2 {
			hopNum, err := strconv.Atoi(matches[1])
			if err != nil {
				logger.Warn("Failed to parse hop number for timeout", "line", line, "error", err)
				continue
			}
			hops = append(hops, Hop{
				HopNumber: hopNum,
				IP:        "*",
				Latency:   -1,
			})
		}
	}
	return hops
}

// reachedDestination attempts to verify if the last hop matches the target IP.
// Since we used -n (no resolve), we can only compare if 'host' was an IP
// or if we resolve 'host' first.
// For now, simpler check: Is the last hop NOT a timeout?
func reachedDestination(hops []Hop, target string) bool {
	if len(hops) == 0 {
		return false
	}
	last := hops[len(hops)-1]
	if last.IP == "*" {
		return false
	}
	// If target matches IP? (omitted for simplicity as we didn't resolve target inside task)
	return true
}

func (t *TracerouteTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"host": {
				Type:        "string",
				Description: "The destination host.",
				Required:    true,
			},
			"max_hops": {
				Type:        "number",
				Description: "Max TTL (default 30).",
				Required:    false,
				Default:     30,
			},
		},
	}
}

func (t *TracerouteTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"hops": {
				Type:        "array",
				Description: "List of hops.",
				Required:    true,
			},
			"raw": {
				Type:        "string",
				Description: "Raw output.",
				Required:    true,
			},
		},
	}
}
