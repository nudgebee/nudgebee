package tools

import (
	"fmt"
	"math/rand"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"strings"
	"time"

	"github.com/lib/pq"
	"golang.org/x/exp/slog"
)

const ToolTriggerImageScan = "trigger_image_scan"

func init() {
	core.RegisterNBToolFactory(ToolTriggerImageScan, func(accountId string) (core.NBTool, error) {
		return ImageScanTool{}, nil
	})
}

type ImageScanTool struct {
}

func (m ImageScanTool) Name() string {
	return ToolTriggerImageScan
}

func (m ImageScanTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m ImageScanTool) Description() string {
	return `Triggers a Image scan on the container image associated with a specified workload in a Kubernetes cluster.

**Usage:**

* Use this tool when you need to assess the security of a workload's container image.
* **Input:** workload_name, and namespace.
* **Output:** Waits for the scan (up to about 7 minutes) and returns each image's result (clean, vulnerable, failed or unscannable) with its most severe open findings. If the scan is still running when the wait ends, it says so; the results then appear in the cluster's Security > Image Scan tab.

**Example Input:**
{
	"workload_name": "frontend",
	"namespace": "default"
}

**Important Notes:**
* The workload name and namespace must be valid.
* Use the returned status to guide further action or suggest remediation.
`
}

func (m ImageScanTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Details to scan image",
			},
		},
		Required: []string{"command"},
	}
}

// imageScanResult is one image's outcome, read from the image_scan_summary row
// the server-run scan writes when that image's scan ends. Status is the
// summary's (clean | vulnerable | failed | unscannable), or imageScanRunning
// while no summary newer than the request exists yet.
type imageScanResult struct {
	Image  string         `json:"image"`
	Status string         `json:"status"`
	Total  int            `json:"total,omitempty"`
	Counts map[string]int `json:"counts,omitempty"`
	Error  string         `json:"error,omitempty"`
}

const (
	imageScanRunning     = "running"
	imageScanFindingsMax = 10
	imageScanTabHint     = "the cluster's Security > Image Scan tab"
)

// imageScanWait bounds how long the tool waits for the scan: exponential
// backoff with jitter, about 7 minutes in all.
var imageScanWait = struct {
	attempts   int
	initial    time.Duration
	max        time.Duration
	multiplier float64
	jitter     float64
}{attempts: 20, initial: 2 * time.Second, max: 30 * time.Second, multiplier: 1.5, jitter: 0.1}

func (m ImageScanTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// The scan stamps scanned_at in whole seconds, so compare from the start
	// of the second the request was made in.
	startTime := time.Now().UTC().Truncate(time.Second)
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return core.NBToolResponse{}, fmt.Errorf("empty command")
	}
	var inputMap map[string]any
	err := common.UnmarshalJson([]byte(command), &inputMap)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("failed to parse command: %w", err)
	}
	workloadName, ok := inputMap["workload_name"].(string)
	if !ok {
		return core.NBToolResponse{}, fmt.Errorf("workload_name is required")
	}
	namespace, ok := inputMap["namespace"].(string)
	if !ok {
		return core.NBToolResponse{}, fmt.Errorf("namespace is required")
	}

	// Security validation to prevent SQL injection and ensure valid K8s names
	if !common.IsValidKubernetesName(workloadName) {
		return core.NBToolResponse{}, fmt.Errorf("invalid workload_name: must strictly follow DNS-1123 label standard (lowercase alphanumeric and hyphens)")
	}
	if !common.IsValidKubernetesName(namespace) {
		return core.NBToolResponse{}, fmt.Errorf("invalid namespace: must strictly follow DNS-1123 label standard (lowercase alphanumeric and hyphens)")
	}

	tenantId, err := security.GetTenantIdFromAccountId(nbRequestContext.AccountId)
	if err != nil {
		fmt.Println("Error getting tenant ID:", err)
		return core.NBToolResponse{}, err
	}

	nbRequestContext.Ctx.GetLogger().Info("security: triggering image scan", "accountId", nbRequestContext.AccountId, "workloadName", workloadName, "namespace", namespace)

	images, err := TriggerImageScanApi(tenantId, nbRequestContext.AccountId, nbRequestContext.UserId, workloadName, namespace)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("Unable to trigger Image Scan", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, err
	}

	// The scan runs on the server, detached from this request, and records
	// each image's outcome in its image_scan_summary row; that row is what
	// says the scan is over.
	results, err := waitForImageScans(nbRequestContext, images, func() (map[string]imageScanResult, error) {
		return queryImageScanSummaries(nbRequestContext.AccountId, images, startTime)
	}, time.Sleep)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("security: reading image scan results failed", "error", err.Error())
		return core.NBToolResponse{
			Data:   fmt.Sprintf("The image scan of workload '%s' in namespace '%s' was started, but its results could not be read: %s. They will appear in %s when the scan finishes.", workloadName, namespace, err, imageScanTabHint),
			Status: core.NBToolResponseStatusError,
		}, err
	}
	return m.scanResponse(nbRequestContext, workloadName, namespace, images, results)
}

// waitForImageScans polls until every image has a result or the wait budget
// is spent, and returns the results it has. A failed poll is retried; it is
// only an error when no poll ever succeeded.
func waitForImageScans(nbRequestContext core.NbToolContext, images []string, poll func() (map[string]imageScanResult, error), sleep func(time.Duration)) (map[string]imageScanResult, error) {
	interval := imageScanWait.initial
	var lastErr error
	var results map[string]imageScanResult
	for attempt := range imageScanWait.attempts {
		got, err := poll()
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("security: polling image scan results failed", "error", err.Error(), "attempt", attempt+1)
			lastErr = err
		} else {
			results, lastErr = got, nil
			if len(results) >= len(images) {
				return results, nil
			}
			nbRequestContext.Ctx.GetLogger().Info("security: image scan still running", "done", len(results), "images", len(images), "attempt", attempt+1)
		}
		if attempt < imageScanWait.attempts-1 {
			var wait time.Duration
			wait, interval = calculateBackoffWaitTime(interval, imageScanWait.jitter, imageScanWait.multiplier, imageScanWait.max)
			sleep(wait)
		}
	}
	if results == nil && lastErr != nil {
		return nil, lastErr
	}
	return results, nil
}

// calculateBackoffWaitTime calculates the wait time with exponential backoff and jitter
func calculateBackoffWaitTime(currentInterval time.Duration, jitterFactor float64, backoffMultiplier float64, maxInterval time.Duration) (time.Duration, time.Duration) {
	// Add jitter: ±jitterFactor% of current interval
	jitter := time.Duration(float64(currentInterval) * jitterFactor * (2*rand.Float64() - 1))
	waitTime := currentInterval + jitter

	// Calculate next interval for exponential backoff
	nextInterval := time.Duration(float64(currentInterval) * backoffMultiplier)
	if nextInterval > maxInterval {
		nextInterval = maxInterval
	}

	return waitTime, nextInterval
}

// queryImageScanSummaries returns the images whose scan ended at or after
// since, keyed by image.
func queryImageScanSummaries(accountId string, images []string, since time.Time) (map[string]imageScanResult, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("queryImageScanSummaries: failed to get database manager: %w", err)
	}
	const query = `SELECT account_object_id, recommendation::text FROM recommendation
		WHERE cloud_account_id = $1
		AND category = 'Security'
		AND rule_name = 'image_scan_summary'
		AND account_object_id = ANY($2)
		AND (recommendation->>'scanned_at')::timestamptz >= $3`
	rows, err := dbManager.Db.Query(query, accountId, pq.Array(images), since)
	if err != nil {
		return nil, fmt.Errorf("queryImageScanSummaries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]imageScanResult{}
	for rows.Next() {
		var image, body string
		if err := rows.Scan(&image, &body); err != nil {
			return nil, fmt.Errorf("queryImageScanSummaries: scan: %w", err)
		}
		r := imageScanResult{Image: image}
		if err := common.UnmarshalJson([]byte(body), &r); err != nil {
			return nil, fmt.Errorf("queryImageScanSummaries: decode %q: %w", image, err)
		}
		r.Image = image
		out[image] = r
	}
	return out, rows.Err()
}

// scanResponse reports every image's outcome and, when any image has
// findings, the most severe open ones. A scan still running when the wait ends
// is not a failure: the answer says where its results will appear.
func (m ImageScanTool) scanResponse(nbRequestContext core.NbToolContext, workloadName, namespace string, images []string, results map[string]imageScanResult) (core.NBToolResponse, error) {
	report := struct {
		Workload  string            `json:"workload"`
		Namespace string            `json:"namespace"`
		Message   string            `json:"message"`
		Images    []imageScanResult `json:"images"`
		Findings  []map[string]any  `json:"findings,omitempty"`
	}{Workload: workloadName, Namespace: namespace}

	var running, failed []string
	vulnerable := false
	for _, image := range images {
		r, ok := results[image]
		if !ok {
			r = imageScanResult{Image: image, Status: imageScanRunning}
			running = append(running, image)
		}
		switch r.Status {
		case "failed", "unscannable":
			failed = append(failed, image)
		case "vulnerable":
			vulnerable = true
		}
		report.Images = append(report.Images, r)
	}

	switch {
	case len(running) > 0:
		report.Message = fmt.Sprintf("The scan is still running for %s. Its results will appear in %s when it finishes; check there or run this tool again later.", strings.Join(running, ", "), imageScanTabHint)
	case len(failed) == len(images):
		report.Message = fmt.Sprintf("The scan failed for every image (%s); see each image's error.", strings.Join(failed, ", "))
	case vulnerable:
		report.Message = fmt.Sprintf("Scan finished. Findings below are the %d most severe open ones; the full list is in %s.", imageScanFindingsMax, imageScanTabHint)
	default:
		report.Message = "Scan finished."
	}

	if vulnerable {
		findings, err := queryOpenImageFindings(nbRequestContext.AccountId, workloadName, namespace)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("security: reading image scan findings failed", "error", err.Error(), "workloadName", workloadName, "namespace", namespace)
			report.Message += fmt.Sprintf(" The findings could not be read here (%s); see %s.", err, imageScanTabHint)
		}
		report.Findings = findings
	}

	data, err := common.MarshalJson(report)
	if err != nil {
		return core.NBToolResponse{Status: core.NBToolResponseStatusError}, fmt.Errorf("trigger_image_scan: marshal: %w", err)
	}
	resp := core.NBToolResponse{
		Data:       string(data),
		Type:       core.NBToolResponseTypeJson,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"security", "image-scan"}, "Check Security Vulnerabilities", nil, "")},
	}
	if len(running) == 0 && len(failed) == len(images) {
		resp.Status = core.NBToolResponseStatusError
		return resp, fmt.Errorf("image scan of workload '%s' in namespace '%s' failed for every image", workloadName, namespace)
	}
	return resp, nil
}

// queryOpenImageFindings returns the workload's most severe open image
// findings.
func queryOpenImageFindings(accountId, workloadName, namespace string) ([]map[string]any, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("queryOpenImageFindings: failed to get database manager: %w", err)
	}
	// Named fields only: the full recommendation carries each CVE's whole
	// description and reference list, several KB per finding.
	query := `SELECT image, vulnerability_id, severity, package_id,
		recommendation::jsonb->>'PkgName' AS package_name,
		recommendation::jsonb->>'InstalledVersion' AS installed_version,
		recommendation::jsonb->>'FixedVersion' AS fixed_version,
		recommendation::jsonb->>'Title' AS title
		FROM (` + SecurityView + `
		AND cr.cloud_account_id = $1::uuid AND cr.workload_name = $2 AND cr.namespace = $3
		AND r.status = 'Open' AND r.rule_name = 'image_scan') f
		ORDER BY CASE severity WHEN 'Critical' THEN 0 WHEN 'High' THEN 1 WHEN 'Medium' THEN 2 WHEN 'Low' THEN 3 ELSE 4 END, vulnerability_id
		LIMIT ` + fmt.Sprint(imageScanFindingsMax)
	var data []map[string]any
	if err := dbManager.QueryAndScan(&data, query, accountId, workloadName, namespace); err != nil {
		return nil, fmt.Errorf("queryOpenImageFindings: %w", err)
	}
	return data, nil
}

// TriggerImageScanApi starts the scan and returns the images it scans.
func TriggerImageScanApi(tenantId, accountId, userId, workloadName, namespace string) ([]string, error) {
	// Construct the ServicesRequest payload
	request := services_server.ScanImageServiceRequest{
		Action: services_server.Action{
			Name: "security_scan_image",
		},
		Input: services_server.ScanImageRequest{
			AccountId: accountId,
			Namespace: namespace,
			Workload:  workloadName,
		},
		SessionVariables: services_server.SessionVariables{
			UserID:       userId,
			UserTenantID: tenantId,
		},
	}

	// Call the Execute function
	result, err := services_server.ExecuteScanImageQuery(request)
	if err != nil {
		return nil, fmt.Errorf("failed to trigger image scan: %w", err)
	}
	// Check if the result is empty
	if len(result) == 0 {
		return nil, fmt.Errorf("no data returned from scan image query")
	}

	// check if the result contains the "result" key
	jsonStr, ok := result["result"]
	if !ok {
		slog.Error("trigger_image_scan: result key not found in response", "result", result)
		return nil, fmt.Errorf("scan triggered, but the response named no images")
	}

	// Define a structure to hold the parsed data
	var parsedData []struct {
		Image string `json:"image"`
	}

	// Parse the JSON
	if err := common.UnmarshalJson([]byte(jsonStr), &parsedData); err != nil {
		slog.Error("trigger_image_scan: error parsing JSON response", "error", err)
		return nil, fmt.Errorf("scan triggered, but its response could not be read: %w", err)
	}

	images := make([]string, 0, len(parsedData))
	for _, item := range parsedData {
		if item.Image != "" {
			images = append(images, item.Image)
		}
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("scan triggered, but the response named no images")
	}
	return images, nil
}

// InferToolRequestType: this tool only triggers a scan, which changes no
// state, so it is always a read. (It used to send the one-word prompt "read"
// to an LLM and rely on the model echoing it back.)
func (m ImageScanTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}
