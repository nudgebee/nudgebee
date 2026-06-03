package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type WorkspaceExecuteRequest struct {
	AccountId  string         `json:"account_id"`
	Tool       string         `json:"tool"`
	Command    string         `json:"command"`
	Arguments  map[string]any `json:"arguments"`
	ConfigName string         `json:"config_name"`
}

type WorkspaceExecuteResponse struct {
	Result any    `json:"result"`
	Error  string `json:"error"`
}

func main() {
	// 1. Read Configuration
	llmServerUrl := os.Getenv("NB_LLM_SERVER_URL")
	accountId := os.Getenv("NB_ACCOUNT_ID")
	token := os.Getenv("NB_WORKSPACE_TOKEN")
	configName := os.Getenv("NB_TOOL_CONFIG_NAME")

	if llmServerUrl == "" || accountId == "" || token == "" {
		fmt.Fprintf(os.Stderr, "Error: Missing required environment variables (NB_LLM_SERVER_URL, NB_ACCOUNT_ID, NB_WORKSPACE_TOKEN)\n")
		os.Exit(1)
	}

	// 2. Identify Tool
	// We use os.Args[0] because os.Executable() resolves symlinks to the target binary (shim),
	// but we need to know the name it was invoked as (e.g., kubectl, helm).
	toolName := filepath.Base(os.Args[0])

	// 3. Construct Command with Proper Quoting
	quote := func(s string) string {
		if s == "" {
			return "''"
		}
		if !strings.ContainsAny(s, " \t\n'\"`$|&;<>()\\") {
			return s
		}
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}

	args := make([]string, 0, len(os.Args))
	// Always use the detected toolName as the first argument (command)
	args = append(args, toolName)
	for _, arg := range os.Args[1:] {
		args = append(args, quote(arg))
	}
	fullCommand := strings.Join(args, " ")

	// 4. Construct Payload
	payload := WorkspaceExecuteRequest{
		AccountId:  accountId,
		Tool:       toolName,
		Command:    fullCommand,
		Arguments:  map[string]any{},
		ConfigName: configName,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to marshal payload: %v\n", err)
		os.Exit(1)
	}

	// 5. Send Request
	url := fmt.Sprintf("%s/api/v1/workspace/execute", strings.TrimRight(llmServerUrl, "/"))
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create request: %v\n", err)
		os.Exit(1)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-Token", token)

	client := &http.Client{
		Timeout: 120 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Request failed: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// 6. Handle Response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to read response: %v\n", err)
		os.Exit(1)
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: Server returned %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var result WorkspaceExecuteResponse
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to parse response: %v\nResponse: %s\n", err, string(body))
		os.Exit(1)
	}

	// 7. Output Result
	if result.Result != nil {
		outputToPrint := ""
		isJSON := false

		switch v := result.Result.(type) {
		case string:
			outputToPrint = v
			// Check if this string is itself a JSON object
			trimmed := strings.TrimSpace(v)
			if strings.HasPrefix(trimmed, "{") {
				var inner map[string]any
				if err := json.Unmarshal([]byte(trimmed), &inner); err == nil {
					stdout, hasStdout := inner["stdout"].(string)
					stderr, hasStderr := inner["stderr"].(string)
					if hasStdout || hasStderr {
						if hasStdout && stdout != "" {
							fmt.Print(stdout)
							if !strings.HasSuffix(stdout, "\n") {
								fmt.Println()
							}
						}
						if hasStderr && stderr != "" {
							fmt.Fprint(os.Stderr, stderr)
							if !strings.HasSuffix(stderr, "\n") {
								fmt.Fprintln(os.Stderr)
							}
						}
						isJSON = true
					}
				}
			}
		case map[string]any:
			// Check for standard execution output format
			stdout, hasStdout := v["stdout"].(string)
			stderr, hasStderr := v["stderr"].(string)

			if hasStdout || hasStderr {
				if hasStdout && stdout != "" {
					fmt.Print(stdout)
					if !strings.HasSuffix(stdout, "\n") {
						fmt.Println()
					}
				}
				if hasStderr && stderr != "" {
					fmt.Fprint(os.Stderr, stderr)
					if !strings.HasSuffix(stderr, "\n") {
						fmt.Fprintln(os.Stderr)
					}
				}
				isJSON = true
			} else {
				// Fallback: pretty print the whole object
				out, _ := json.MarshalIndent(v, "", "  ")
				outputToPrint = string(out)
			}
		default:
			outputToPrint = fmt.Sprintf("%v", v)
		}

		if !isJSON && outputToPrint != "" {
			fmt.Print(outputToPrint)
			if !strings.HasSuffix(outputToPrint, "\n") {
				fmt.Println()
			}
		}
	}

	if result.Error != "" {
		fmt.Fprintf(os.Stderr, "Error from tool: %s\n", result.Error)
		os.Exit(1)
	}
}
