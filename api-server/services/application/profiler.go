package application

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type AgentTask struct {
	ID       string `db:"id"`
	Status   []byte `db:"status"`   // note: []byte, not string
	Response []byte `db:"response"` // JSONB => []byte
}

func AppplicationProfile(ctx *security.RequestContext, request ApplicationProfileRequest) (*ApplicationProfileResponse, error) {

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("error getting database manager", "error", err)
		return nil, err
	}
	accountId := request.AccountId

	profileDuration := 60
	if request.ProfileDuration > 0 {
		profileDuration = request.ProfileDuration
	}

	params := map[string]any{
		"name":         request.PodName,
		"namespace":    request.Namespace,
		"seconds":      profileDuration,
		"profile_type": request.ProfileType,
	}

	if request.ProfileTool != "" {
		tool := ProfilingTool(request.ProfileTool)
		if !tool.IsValid() {
			return nil, fmt.Errorf("invalid profiling tool: %q", request.ProfileTool)
		}
		params["profile_tool"] = request.ProfileTool
	}
	if request.ApplicationLanguage != "" {
		params["lang"] = request.ApplicationLanguage
	}
	if request.OutputType != "" {
		params["output_type"] = request.OutputType
	}

	payload := map[string]any{
		"sinks":         nil,
		"no_sinks":      true,
		"sync_response": true,
		"origin":        "profiler",
		"timestamp":     time.Now(),
		"action_name":   "pod_profiler",
		"action_params": params,
	}
	payloadStr, err := common.MarshalJson(payload)
	if err != nil {
		ctx.GetLogger().Error("error marshalling payload", "error", err)
		return nil, err
	}
	tasks := make([]map[string]any, 0)
	task := map[string]any{
		"id":               common.GenerateUUID(),
		"cloud_account_id": accountId,
		"tenant":           ctx.GetSecurityContext().GetTenantId(),
		"action":           "pod_profiler",
		"payload":          string(payloadStr),
		"status":           "TODO",
		"source":           "profiler",
	}

	tasks = append(tasks, task)
	if (len(tasks)) == 0 {
		return nil, fmt.Errorf("no tasks to insert")
	}

	_, err = dbms.Db.NamedExec(`INSERT INTO agent_task (id, cloud_account_id, tenant, action, payload, status, source) values (:id, :cloud_account_id, :tenant, :action, :payload, :status, :source)`, tasks)
	if err != nil {
		ctx.GetLogger().Error("error inserting image scanner tasks", "error", err)
		return nil, err
	}
	profileTaskId := tasks[0]["id"].(string)
	profileResponse := &ApplicationProfileResponse{
		ProfileTaskId: profileTaskId,
		AccountId:     accountId,
		Status:        "TODO",
	}
	startProfileWatcher(ctx, dbms.Db, &request, profileTaskId) // nolint:errcheck
	return profileResponse, nil
}

func GetProfileStatus(
	ctx *security.RequestContext,
	request *GetApplicationProfileRequest,
) (*ApplicationProfileResponse, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("error getting database manager", "error", err)
		return nil, err
	}

	accountID := request.AccountId
	taskID := request.ProfileTaskId

	// pull back exactly one row
	var t AgentTask
	err = dbms.Db.
		QueryRowx(
			`SELECT id, status, response
             FROM agent_task
             WHERE id = $1
               AND cloud_account_id = $2`,
			taskID,
			accountID,
		).
		StructScan(&t)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			ctx.GetLogger().Error("no profile task found", "profile_task_id", taskID)
			return nil, fmt.Errorf("no profile task found with id: %s", taskID)
		}
		ctx.GetLogger().Error("error querying profile task", "error", err)
		return nil, err
	}

	status := string(t.Status)
	respStr := string(t.Response)

	switch status {
	case "COMPLETED":
		if respStr == "" {
			ctx.GetLogger().Error("no profile evidence found", "profile_task_id", taskID)
			return nil, fmt.Errorf("no profile evidence found for task id: %s", taskID)
		}

		// unmarshal the JSONB payload into a map
		var ev map[string]any
		if err := common.UnmarshalJsonString(respStr, &ev); err != nil {
			ctx.GetLogger().Error("error unmarshalling evidence", "error", err)
			return nil, err
		}

		var datamap = map[string]any{
			"data": ev,
		}
		return &ApplicationProfileResponse{
			ProfileTaskId: taskID,
			AccountId:     accountID,
			Status:        status,
			Profile:       datamap,
		}, nil

	case "FAILED":
		if respStr == "" {
			ctx.GetLogger().Error("no profile evidence found", "profile_task_id", taskID)
			return nil, fmt.Errorf("no profile evidence found for task id: %s", taskID)
		}

		var ev map[string]any
		if err := common.UnmarshalJsonString(respStr, &ev); err != nil {
			ctx.GetLogger().Error("error unmarshalling evidence", "error", err)
			return nil, err
		}

		if msg, exists := ev["msg"]; exists {
			ctx.GetLogger().Error(
				"profile task failed",
				"profile_task_id", taskID,
				"error", msg,
			)
			return &ApplicationProfileResponse{
				ProfileTaskId: taskID,
				AccountId:     accountID,
				Status:        status,
				ErrorMessage:  fmt.Sprintf("%v", msg),
			}, nil
		}

		ctx.GetLogger().Error("profile task failed", "profile_task_id", taskID)
		return nil, fmt.Errorf("profile task failed with id: %s", taskID)

	default:
		// still in-progress or other status
		return &ApplicationProfileResponse{
			ProfileTaskId: taskID,
			AccountId:     accountID,
			Status:        status,
		}, nil
	}
}

func ConvertProfile(context *security.RequestContext, request ApplicationProfileConvertRequest) (ApplicationProfileConvertResponse, error) {
	if request.Profile == "" {
		return ApplicationProfileConvertResponse{}, fmt.Errorf("profile is required")
	}
	svgProfile := ""
	var err error
	if request.ResponseFormat != "svg" {

		svgProfile, err = Base64PprofToSVGGz(*context.GetLogger(), request.Profile)
	} else if request.ResponseFormat != "raw" {
		svgProfile, err = Base64PprofToRaw(*context.GetLogger(), request.Profile)
	} else {
		return ApplicationProfileConvertResponse{}, fmt.Errorf("unsupported response format: %s", request.ResponseFormat)
	}
	if err != nil {
		return ApplicationProfileConvertResponse{}, err
	}
	return ApplicationProfileConvertResponse{
		SvgProfile: svgProfile,
	}, nil

}

func Base64PprofToSVGGz(logger slog.Logger, b64Profile string) (string, error) {
	// 1) Decode the Base64‐encoded pprof (.pb.gz)
	profData, err := base64.StdEncoding.DecodeString(b64Profile)
	if err != nil {
		logger.Error("Failed to decode base64 profile", "error", err)
		return "", fmt.Errorf("failed to decode base64 profile: %w", err)
	}

	// 2) Write the compressed profile to a .pb.gz temp file
	tmpDir := os.TempDir()
	gzPath := filepath.Join(tmpDir, fmt.Sprintf("pprof-%d.pb.gz", time.Now().UnixNano()))
	if err := os.WriteFile(gzPath, profData, 0600); err != nil {
		logger.Error("Failed to write compressed profile file", "path", gzPath, "error", err)
		return "", fmt.Errorf("failed to write compressed profile: %w", err)
	}
	defer func() {
		err := os.Remove(gzPath)
		if err != nil {
			logger.Error("Failed to remove compressed profile file", "path", gzPath, "error", err)
		}
	}()

	// 3) Decompress .pb.gz → .pb
	pbPath := strings.TrimSuffix(gzPath, ".gz")
	inFile, err := os.Open(gzPath)
	if err != nil {
		logger.Error("Failed to open compressed profile file", "error", err)
		return "", fmt.Errorf("failed to open compressed profile: %w", err)
	}
	defer func() {
		err := inFile.Close()
		if err != nil {
			logger.Error("Failed to close compressed profile file", "error", err)
		}
	}()

	gzReader, err := gzip.NewReader(inFile)
	if err != nil {
		logger.Error("Failed to create gzip reader", "error", err)
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() {
		err := gzReader.Close()
		if err != nil {
			logger.Error("Failed to close gzip reader", "error", err)
		}
	}()

	outFile, err := os.Create(pbPath)
	if err != nil {
		logger.Error("Failed to create decompressed profile file", "path", pbPath, "error", err)
		return "", fmt.Errorf("failed to create decompressed profile: %w", err)
	}
	defer func() {
		err := outFile.Close()
		if err != nil {
			logger.Error("Failed to close decompressed profile file", "error", err)
		}
	}()
	defer func() {
		err := os.Remove(pbPath)
		if err != nil {
			logger.Error("Failed to remove decompressed profile file", "path", pbPath, "error", err)
		}
	}()

	if _, err := io.Copy(outFile, gzReader); err != nil {
		logger.Error("Failed to decompress profile data", "error", err)
		return "", fmt.Errorf("failed to decompress profile data: %w", err)
	}

	// 4) Run `go tool pprof -svg` → .svg
	svgPath := pbPath + ".svg"
	svgFile, err := os.Create(svgPath)
	if err != nil {
		logger.Error("Failed to create svg output file", "path", svgPath, "error", err)
		return "", fmt.Errorf("failed to create svg output file: %w", err)
	}
	defer func() {
		err := svgFile.Close()
		if err != nil {
			logger.Error("Failed to close svg output file", "error", err)
		}
	}()
	defer func() {
		err := os.Remove(svgPath)
		if err != nil {
			logger.Error("Failed to remove svg output file", "path", svgPath, "error", err)
		}
	}()

	cmd := exec.Command("pprof", "-svg", pbPath)
	cmd.Stdout = svgFile
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		logger.Error("pprof command failed", "error", err)
		return "", fmt.Errorf("pprof command failed: %w", err)
	}

	// 5) Read the SVG and re-compress to .svg.gz in memory
	rawSVG, err := os.ReadFile(svgPath)
	if err != nil {
		logger.Error("Failed to read svg file", "error", err)
		return "", fmt.Errorf("failed to read svg file: %w", err)
	}

	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	if _, err := gzipWriter.Write(rawSVG); err != nil {
		func() {
			err := gzipWriter.Close()
			if err != nil {
				logger.Error("Failed to close gzip writer", "error", err)
			}
		}()
		logger.Error("Failed to gzip SVG data", "error", err)
		return "", fmt.Errorf("failed to gzip svg data: %w", err)
	}
	func() {
		err := gzipWriter.Close()
		if err != nil {
			logger.Error("Failed to close gzip writer", "error", err)
		}
	}()

	// 6) Return the gzipped SVG as a Base64 string
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func Base64PprofToRaw(logger slog.Logger, b64Profile string) (string, error) {
	// 1) Decode the Base64‐encoded pprof (.pb.gz)
	profData, err := base64.StdEncoding.DecodeString(b64Profile)
	if err != nil {
		logger.Error("Failed to decode base64 profile", "error", err)
		return "", fmt.Errorf("failed to decode base64 profile: %w", err)
	}

	// 2) Write the compressed profile to a .pb.pprof temp file
	tmpDir := os.TempDir()

	pprofPath := filepath.Join(tmpDir, fmt.Sprintf("pprof-%d.pb.pprof", time.Now().UnixNano()))
	if err := os.WriteFile(pprofPath, profData, 0600); err != nil {
		logger.Error("Failed to write compressed profile file", "path", pprofPath, "error", err)
		return "", fmt.Errorf("failed to write compressed profile: %w", err)
	}
	defer func() {
		err := os.Remove(pprofPath)
		if err != nil {
			logger.Error("Failed to remove pprof file", "path", pprofPath, "error", err)
		}
	}()

	// 3) Decompress .pb.gz → .pb
	pbPath := pprofPath + ".pprof"
	inFile, err := os.Open(pprofPath)
	if err != nil {
		logger.Error("Failed to open compressed profile file", "error", err)
		return "", fmt.Errorf("failed to open compressed profile: %w", err)
	}
	defer func() {
		err := inFile.Close()
		if err != nil {
			logger.Error("Failed to close compressed profile file", "error", err)
		}
	}()

	gzReader, err := gzip.NewReader(inFile)
	if err != nil {
		logger.Error("Failed to create gzip reader", "error", err)
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() {
		err := gzReader.Close()
		if err != nil {
			logger.Error("Failed to close gzip reader", "error", err)
		}
	}()

	outFile, err := os.Create(pbPath)
	if err != nil {
		logger.Error("Failed to create decompressed profile file", "path", pbPath, "error", err)
		return "", fmt.Errorf("failed to create decompressed profile: %w", err)
	}
	defer func() {
		err := outFile.Close()
		if err != nil {
			logger.Error("Failed to close decompressed profile file", "error", err)
		}
	}()
	defer func() {
		err := os.Remove(pbPath)
		if err != nil {
			logger.Error("Failed to remove decompressed profile file", "path", pbPath, "error", err)
		}
	}()

	if _, err := io.Copy(outFile, gzReader); err != nil {
		logger.Error("Failed to decompress profile data", "error", err)
		return "", fmt.Errorf("failed to decompress profile data: %w", err)
	}

	cmd := exec.Command("go", "tool", "pprof", "-noinlines", "-raw", pbPath)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		logger.Error("pprof command failed", "error", err)
		return "", fmt.Errorf("pprof command failed: %w", err)
	}
	return out.String(), nil
}

func startProfileWatcher(
	ctx *security.RequestContext,
	db *sqlx.DB,
	request *ApplicationProfileRequest,
	taskID string,
) error {
	tenantID := ctx.GetSecurityContext().GetTenantId()
	accountID := request.AccountId

	pods := make([]map[string]interface{}, 0)
	rows, err := db.Queryx(`select workload_name from k8s_pods where name=$1 and namespace=$2 and cloud_account_id = $3`, request.PodName, request.Namespace, request.AccountId)
	if err != nil {
		return err
	}
	if rows != nil {
		defer func() {
			err := rows.Close()
			if err != nil {
				ctx.GetLogger().Error("error closing rows", "error", err)
			}
		}()
		for rows.Next() {
			var pod = make(map[string]interface{})
			err = rows.MapScan(pod)
			if err != nil {
				return err
			}
			pods = append(pods, pod)
		}
	}
	if len(pods) == 0 {
		return nil
	}

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			resp, err := GetProfileStatus(ctx, &GetApplicationProfileRequest{
				AccountId:     accountID,
				ProfileTaskId: taskID,
			})
			if err != nil {
				ctx.GetLogger().Error("error fetching profile status", "error", err, "task_id", taskID)
				continue
			}

			switch resp.Status {
			case "COMPLETED":
				profile, err := relay.FormatEvidenceResponseFromAgent("Pod Profiler", resp.Profile)

				if err != nil {
					ctx.GetLogger().Error("error formatting evidence response", "error", err, "task_id", taskID)
					return
				}

				profileList := []map[string]any{}
				profileList = append(profileList, profile)

				blob, err := common.MarshalJson(profileList)
				if err != nil {
					ctx.GetLogger().Error("error marshalling profile data", "error", err, "task_id", taskID)
					return
				}

				record := map[string]any{
					"id":               taskID,
					"tenant_id":        tenantID,
					"cloud_account_id": accountID,
					"pod_name":         request.PodName,
					"workload_name":    pods[0]["workload_name"],
					"namespace":        request.Namespace,
					"created_by":       ctx.GetSecurityContext().GetUserId(),
					"profile":          string(blob),
					"profile_type":     request.ProfileType,
					"profile_tool":     request.ProfileTool,
					"output_type":      request.OutputType,
					"profile_duration": request.ProfileDuration,
					"profile_language": request.ApplicationLanguage,
					"source":           "profiler",
					"source_id":        taskID,
				}
				_, err = db.NamedExec(
					`INSERT INTO application_profile
                     (id, tenant_id, cloud_account_id, pod_name, workload_name,
                      namespace, created_by, profile, profile_type, profile_tool, output_type,
                      profile_duration, profile_language, source, source_id)
                     VALUES
                     (:id, :tenant_id, :cloud_account_id, :pod_name, :workload_name,
                      :namespace, :created_by, :profile, :profile_type, :profile_tool, :output_type,
                      :profile_duration, :profile_language, :source, :source_id)`,
					record,
				)
				if err != nil {
					ctx.GetLogger().Error("error inserting into application_profile", "error", err, "task_id", taskID)
				}
				return

			case "FAILED":
				ctx.GetLogger().Error("profile task failed, skipping persistence", "task_id", taskID)
				return
			case "TODO":
				ctx.GetLogger().Debug("profile task still in todo", "task_id", taskID)
			case "PROCESSING":
				ctx.GetLogger().Debug("profile task still running", "task_id", taskID)
			case "TIMEOUT":
				ctx.GetLogger().Error("profile task timed out, skipping persistence", "task_id", taskID)
				return
			default:
				ctx.GetLogger().Error("unknown profile status", "status", resp.Status, "task_id", taskID)
				return
			}
		}
	}()
	return nil
}
