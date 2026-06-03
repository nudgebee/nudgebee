package ml

import (
	"fmt"
	"io"
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"strings"
	"time"
)

func RunVerticalRightsize(body VerticalRightsizeBody) (*VerticalRightSizeResponse, error) {
	if config.Config.MlServerUrl == "" {
		return nil, fmt.Errorf("ml: ml server url not set")
	}

	url := config.Config.MlServerUrl
	if !strings.HasSuffix(url, "/") {
		url += "/"
	}
	url += "rightsizing/vertical"

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// ML inference can legitimately take a while on busy clusters. Without
	// an explicit timeout the http.Client falls back to Go's 30s TCP dial
	// default with no overall deadline, so a stuck downstream would hang
	// the workflow. Reuse the relay-command budget — same shape (outbound
	// long-ish call) and ops already know how to bump it cluster-wide.
	timeout := time.Duration(config.Config.RunbookServerRelayCommandExecutionTimeoutSeconds) * time.Second

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(body), common.HttpWithTimeout(timeout))
	if err != nil {
		slog.Error("unable to access ml server", "error", err)
		return nil, fmt.Errorf("unable to access ml server %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("ml: failed to close response body", "error", err)
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		slog.Error("ml server returned error", "status", resp.StatusCode, "body", string(jsonBody))
		return nil, fmt.Errorf("ml server returned status %d", resp.StatusCode)
	}

	var response VerticalRightSizeResponse
	if err := common.UnmarshalJson(jsonBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ml response: %w", err)
	}

	return &response, nil
}
