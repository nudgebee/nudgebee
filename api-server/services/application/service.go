package application

import (
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strings"
	"time"
)

func CompareApplicationDeployment(context *security.RequestContext, request ApplicationDeploymentCompareRequest) ([]ApplicationDeploymentInsight, error) {
	// get last deployment from database

	if request.AccountId == "" {
		return []ApplicationDeploymentInsight{}, fmt.Errorf("account id is required")
	}

	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return []ApplicationDeploymentInsight{}, fmt.Errorf("unauthorized")
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		return []ApplicationDeploymentInsight{}, fmt.Errorf("unauthorized")
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []ApplicationDeploymentInsight{}, err
	}

	insights := make([]ApplicationDeploymentInsight, 0)
	for _, app := range request.Applications {
		workload := app.Name
		namespace := app.Namespace
		kind := app.Kind
		accountId := request.AccountId
		events := make([]map[string]interface{}, 0)
		rows, err := dbms.Db.Queryx(`select * from events where subject_name=$1 and subject_namespace=$2 and subject_type=$3 and finding_type='configuration_change' and cloud_account_id = $4 order by created_at desc limit 2`, workload, namespace, strings.ToLower(kind), accountId)
		if err != nil {
			return []ApplicationDeploymentInsight{}, err
		}
		if rows != nil {
			defer func() {
				err := rows.Close()
				if err != nil {
					slog.Error("anomaly: failed to close rows", "error", err)
				}
			}()

			for rows.Next() {
				var event = make(map[string]interface{})
				err = rows.MapScan(event)
				if err != nil {
					return []ApplicationDeploymentInsight{}, err
				}
				events = append(events, event)
			}
		}
		if len(events) == 0 {
			return []ApplicationDeploymentInsight{}, nil
		}
		// if deployment age is less than 30 minutes, return
		if time.Since(events[0]["created_at"].(time.Time)).Minutes() < 30 {
			return []ApplicationDeploymentInsight{}, nil
		}

		lastDeploymentDateTime := time.Now().Format("2006-01-02T15:04:05.000000Z")
		if len(events) > 0 {
			lastDeploymentDateTime = events[0]["created_at"].(time.Time).Format("2006-01-02T15:04:05.000000Z")
		}
		applications := []ApplicationRequest{
			{
				Name:      workload,
				Namespace: namespace,
			},
		}
		previousStats := make([]relay.ApplicationStatsResponse, 0)
		if len(events) > 1 {

			metricsRequest := ApplicationMetricsRequest{
				Applications: applications,
				StartAt:      events[1]["created_at"].(time.Time),
				EndAt:        events[0]["created_at"].(time.Time),
				AccountId:    accountId,
			}
			previousStats, err = getApplicationMetrics(metricsRequest)
			if err != nil {
				return []ApplicationDeploymentInsight{}, err
			}
		}

		metricsRequest := ApplicationMetricsRequest{
			Applications: applications,
			StartAt:      events[0]["created_at"].(time.Time),
			EndAt:        time.Now(),
			AccountId:    accountId,
		}

		currentstats, err := getApplicationMetrics(metricsRequest)

		if err != nil {
			return []ApplicationDeploymentInsight{}, err
		}

		insight := ApplicationDeploymentInsight{
			Name:                   workload,
			Namespace:              namespace,
			LastDeploymentDateTime: lastDeploymentDateTime,
			PreviousStats:          previousStats[0],
			CurrentStats:           currentstats[0],
		}
		insights = append(insights, insight)
	}

	return insights, nil
}

func GetApplicationMetrics(context *security.RequestContext, applicationMetricRequest ApplicationMetricsRequest) ([]relay.ApplicationStatsResponse, error) {
	if !context.GetSecurityContext().HasAccountAccess(applicationMetricRequest.AccountId, security.SecurityAccessTypeRead) {
		return nil, fmt.Errorf("unauthorized")
	}
	return getApplicationMetrics(applicationMetricRequest)
}

func getApplicationMetrics(applicationMetricRequest ApplicationMetricsRequest) ([]relay.ApplicationStatsResponse, error) {
	accountId := applicationMetricRequest.AccountId
	var builder strings.Builder
	builder.WriteString("[")
	for i, app := range applicationMetricRequest.Applications {
		fmt.Fprintf(&builder, `{"name":"%s","namespace":"%s"}`, app.Name, app.Namespace)
		if i < len(applicationMetricRequest.Applications)-1 {
			builder.WriteString(",")
		}
	}
	builder.WriteString("]")
	applicationStr := builder.String()
	if applicationMetricRequest.StartAt.IsZero() {
		applicationMetricRequest.StartAt = time.Now().AddDate(0, 0, -1)
	}
	if applicationMetricRequest.EndAt.IsZero() {
		applicationMetricRequest.EndAt = time.Now()
	}
	rStartTime := applicationMetricRequest.StartAt.Format("2006-01-02T15:04:05.000000Z")
	rEndTime := applicationMetricRequest.EndAt.Format("2006-01-02T15:04:05.000000Z")
	request :=
		relay.ActionExecuteBody{
			AccountID:  accountId,
			ActionName: "application_stats",
			ActionParams: map[string]any{
				"applications": applicationStr,
				"r_start_time": rStartTime,
				"r_end_time":   rEndTime,
			},
		}

	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    request,
	})

	if err != nil {
		slog.Error("Failed to execute relay task", "error", resp["response"], "accountId", accountId)
		return nil, err
	}

	if resp["status_code"] == 500 {
		slog.Error("Failed to execute relay task", "error", resp["response"], "accountId", accountId)
		return nil, fmt.Errorf("application: failed to execute relay task error %s accountId %s ", resp["response"], accountId)
	}

	reports := make([]relay.ApplicationStatsResponse, 0)
	sloReports := resp["data"].(map[string]any)["data"]
	for _, m := range sloReports.([]any) {
		jsonData, err := common.MarshalJson(m)
		if err != nil {
			continue
		}

		var sloReport relay.ApplicationStatsResponse
		if err := common.UnmarshalJson(jsonData, &sloReport); err != nil {
			continue
		}
		reports = append(reports, sloReport)
	}
	return reports, nil
}

func ListApplications(context *security.RequestContext, accountId string) ([]Application, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []Application{}, err
	}

	rows, err := dbms.Query(`select namespace, name, kind, cloud_resource_id, labels::text, last_seen, creation_time, ready_pods, total_pods from k8s_workloads where cloud_account_id = $1 and is_active = true`, accountId)
	if err != nil {
		return []Application{}, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("anomaly: failed to close rows", "error", err)
		}
	}()

	apps := []Application{}
	for rows.Next() {
		var app Application
		var labelsStr *string
		err = rows.Scan(&app.K8sNamespace, &app.Name, &app.K8sKind, &app.Id, &labelsStr, &app.LastSeenAt, &app.CreatedAt, &app.ReadyPods, &app.TotalPods)
		if err != nil {
			return []Application{}, err
		}
		if app.K8sNamespace != "" {
			app.Arn = fmt.Sprintf("k8s://%s/%s/%s", app.K8sNamespace, app.K8sKind, app.Name)
		} else {
			app.Arn = fmt.Sprintf("k8s://%s/%s", app.K8sKind, app.Name)
		}
		if labelsStr != nil {
			var labels map[string]string
			err = common.UnmarshalJson([]byte(*labelsStr), &labels)
			if err != nil {
				context.GetLogger().Error("unable to parse labels", "error", err, "k8s_namespace", app.K8sNamespace, "name", app.Name, "k8s_kind", app.K8sKind)
			}
			app.Labels = labels
		}
		apps = append(apps, app)
	}

	return apps, nil
}

func GetTraceServiceMap(context *security.RequestContext, request TraceServiceMapRequest) (*ServiceMap, error) {
	if request.AccountID == "" {
		return nil, fmt.Errorf("account_id is required")
	}

	if context.GetSecurityContext().GetUserId() == "" || context.GetSecurityContext().GetTenantId() == "" {
		return nil, fmt.Errorf("unauthorized")
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountID, security.SecurityAccessTypeRead) {
		return nil, fmt.Errorf("unauthorized")
	}

	params := TraceQueryParams{
		WorkloadName:      request.WorkloadName,
		WorkloadNamespace: request.WorkloadNamespace,
		StartTime:         request.StartTime,
		EndTime:           request.EndTime,
		AccountID:         request.AccountID,
		LabelFilters:      request.LabelFilter,
	}

	return FetchTracesAndBuildServiceMap(context, params)
}
