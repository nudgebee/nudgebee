package cloud

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/security"
)

// AccountCredentials is an environment that authenticates a process as a cloud
// account, and when it stops working.
type AccountCredentials struct {
	Env map[string]string
	// ExpiresAt is zero for credentials that cannot expire — a stored access
	// key, or an Azure service principal. Callers surface that difference
	// rather than hiding it: a credential handed to a customer-authored script
	// is a different proposition when waiting does not revoke it.
	ExpiresAt time.Time
}

// Expiring reports whether these credentials revoke themselves.
func (c AccountCredentials) Expiring() bool { return !c.ExpiresAt.IsZero() }

// GetAccountCredentials asks cloud-collector for credentials that authenticate
// a process as the account. duration is how long the caller needs them;
// cloud-collector clamps it to what the account's credential type allows.
//
// The returned environment is secret. It must not be logged, echoed into task
// output, or stored anywhere that outlives the run it was fetched for.
func GetAccountCredentials(ctx *security.RequestContext, accountID string, duration time.Duration) (AccountCredentials, error) {
	if accountID == "" {
		return AccountCredentials{}, errors.New("cloud: account id is required")
	}
	if config.Config.CloudCollectorServerUrl == "" {
		return AccountCredentials{}, errors.New("cloud: cloud collector server url not set")
	}

	headersMap := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
		"x-tenant-id":  ctx.GetSecurityContext().GetTenantId(),
		"x-user-id":    ctx.GetSecurityContext().GetUserId(),
	}
	if config.Config.CloudCollectorServerToken != "" {
		headersMap["X-ACTION-TOKEN"] = config.Config.CloudCollectorServerToken
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/v1/cloud/cli_credentials", config.Config.CloudCollectorServerUrl),
		common.HttpWithHeaders(headersMap),
		common.HttpWithJsonBody(map[string]any{
			"account_id":       accountID,
			"duration_seconds": int(duration.Seconds()),
		}),
	)
	if err != nil {
		return AccountCredentials{}, fmt.Errorf("cloud: unable to reach cloud server: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("cloud: failed to close response body", "error", err)
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return AccountCredentials{}, err
	}
	// Status first: a proxy or ingress error returns HTML, and parsing that as
	// JSON would report a syntax error instead of the 502 worth acting on.
	responseData := map[string]any{}
	parseErr := common.UnmarshalJson(jsonBody, &responseData)
	if resp.StatusCode != 200 {
		// The body carries credentials on success, so failures are reported by
		// status and the provider's own message — never by dumping the response
		// the way the command path does.
		return AccountCredentials{}, fmt.Errorf("cloud: could not get credentials for this account: %s", credentialsError(responseData, resp.StatusCode))
	}
	if parseErr != nil {
		return AccountCredentials{}, parseErr
	}

	data, ok := responseData["data"].(map[string]any)
	if !ok {
		return AccountCredentials{}, errors.New("cloud: credentials response had no data")
	}
	rawEnv, ok := data["env"].(map[string]any)
	if !ok || len(rawEnv) == 0 {
		return AccountCredentials{}, errors.New("cloud: credentials response carried no environment")
	}

	credentials := AccountCredentials{Env: make(map[string]string, len(rawEnv))}
	for k, v := range rawEnv {
		value, ok := v.(string)
		if !ok {
			return AccountCredentials{}, fmt.Errorf("cloud: credential %s is not a string", k)
		}
		credentials.Env[k] = value
	}
	if expiry, ok := data["expires_at"].(string); ok && expiry != "" {
		parsed, err := time.Parse(time.RFC3339, expiry)
		if err != nil {
			return AccountCredentials{}, fmt.Errorf("cloud: could not read credential expiry %q: %w", expiry, err)
		}
		credentials.ExpiresAt = parsed
	}
	return credentials, nil
}

// credentialsError pulls the provider's message out of an error response, so a
// step fails with "assume role ... AccessDenied" rather than a status code.
func credentialsError(responseData map[string]any, statusCode int) string {
	errorsArr, ok := responseData["errors"].([]any)
	if !ok || len(errorsArr) == 0 {
		return fmt.Sprintf("cloud collector returned status %d", statusCode)
	}
	errorMap, ok := errorsArr[0].(map[string]any)
	if !ok {
		return fmt.Sprintf("cloud collector returned status %d", statusCode)
	}
	message, ok := errorMap["message"].(string)
	if !ok || message == "" {
		return fmt.Sprintf("cloud collector returned status %d", statusCode)
	}
	return message
}
