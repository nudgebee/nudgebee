package application

import (
	"fmt"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strings"
	"time"

	"github.com/google/uuid"
)

func Discover(ctx *security.RequestContext) {
	tenants, err := tenant.ListTenants(ctx)
	if err != nil {
		ctx.GetLogger().Error("discovery: unable to list tenants", "error", err)
		return
	}

	for _, tenant := range tenants {
		accounts, err := account.ListActiveAccountsWithConnectedAgents(ctx, tenant.Id)
		if err != nil {
			ctx.GetLogger().Error("discovery: unable to list accounts", "error", err, "tenant", tenant.Id)
			continue
		}

		for _, account := range accounts {
			err := discoverAndUpdateFrameworkAndDashboardAttributes(ctx, tenant.Id, account.Id)
			if err != nil {
				ctx.GetLogger().Error("discovery: unable to identify framework and dashboard", "error", err, "tenant", tenant.Id, "account", account.Id)
			}

			err = discoverAndUpdateExternalApps(ctx, tenant.Id, account.Id)
			if err != nil {
				ctx.GetLogger().Error("discovery: unable to identify external apps", "error", err, "tenant", tenant.Id, "account", account.Id)
			}

			err = discoverAndUpdateRelationships(ctx, tenant.Id, account.Id)
			if err != nil {
				ctx.GetLogger().Error("discovery: unable to identify relationships", "error", err, "tenant", tenant.Id, "account", account.Id)
			}

			err = discoverAndUpdateExternalVMs(ctx, tenant.Id, account.Id)
			if err != nil {
				ctx.GetLogger().Error("discovery: unable to identify hosts", "error", err, "tenant", tenant.Id, "account", account.Id)
			}

		}
	}

}

func discoverAndUpdateExternalVMs(ctx *security.RequestContext, tenantId, accountId string) error {
	endDate := time.Now().UTC()
	startDate := endDate.Add(time.Minute * -30)
	externalAppMap, err := relay.ExecutePrometheus(accountId, startDate, endDate, map[string]string{
		"host": `sum by (host.name, host.ip) (system.memory.utilization{host.name!=""})`,
	}, false)
	if err != nil {
		return err
	}
	hostNameAndIp := map[string]string{}
	for _, query := range externalAppMap {
		seriesListResult := query.(map[string]any)["series_list_result"]
		if seriesListResult != nil {
			for _, seriesAny := range seriesListResult.([]any) {
				series, ok := seriesAny.(map[string]any)
				if !ok {
					continue
				}
				metrixAny := series["metric"]
				if metrixAny == nil {
					continue
				}
				hostName := metrixAny.(map[string]any)["host.name"]
				if hostName == nil {
					continue
				}
				hostIp := metrixAny.(map[string]any)["host.ip"]
				hostIpStr := ""
				if hostIp != nil {
					hostIpStr = hostIp.(string)
				}
				hostNameAndIp[hostName.(string)] = hostIpStr
			}
		}
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	resourceMap := map[string]string{}

	resourceRows, err := dbms.Query("select id::text, resourse_id from cloud_resourses where account = $1 and type = $2 and service_name = $3", accountId, "External", "host")
	if err != nil {
		ctx.GetLogger().Error("discovery: unable to list external resources", "error", err, "tenant", tenantId, "account", accountId)
		return err
	}
	defer func() {
		err := resourceRows.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing resourceRows", "error", err)
		}
	}()

	for resourceRows.Next() {
		var id, resourceId string
		err = resourceRows.Scan(&id, &resourceId)
		if err != nil {
			ctx.GetLogger().Error("discovery: unable to scan row for external resources", "error", err, "tenant", tenantId, "account", accountId)
			return err
		}
		resourceMap[resourceId] = id
	}

	foundResources := map[string]map[string]string{}

	namespace := "external"
	resourceType := "External"
	for host, ip := range hostNameAndIp {
		resourceId := fmt.Sprintf("%s/%s/%s", namespace, resourceType, strings.ReplaceAll(strings.ReplaceAll(host, ":", "-"), ".", "-"))
		id := resourceMap[resourceId]
		tags := fmt.Sprintf(`{"framework":"host", "source": "otel", "ip": "%s"}`, ip)

		if id == "" {
			id = uuid.NewString()
			now := time.Now().UTC()
			_, err := dbms.Exec(`INSERT INTO public.cloud_resourses
					(id, created_at, updated_at, resourse_id, "name", "type", status, account, cloud_provider, region, arn, tenant, tags, meta, service_name, first_seen, last_seen, is_active, external_resource_id)
					VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`, id, now, now, resourceId, host, resourceType, "Active", accountId, "K8s", "global", "k8s://"+resourceId, tenantId, tags, "{}", "host", now, now, true, resourceId)
			if err != nil {
				slog.Error("discovery: external resource update failed", "error", err)
				continue
			}
			err = insertOrUpdateResourceDiscovery(ctx, dbms, tenantId, accountId, id, frameworkDiscovery{
				applicationType: "host",
				namespace:       namespace,
				application:     host,
				container:       "",
				dashboards: []string{
					"host",
				},
			})
			if err != nil {
				ctx.GetLogger().Error("discovery: unable to insert resource discovery", "error", err)
			}
		} else {
			foundResources[resourceId] = map[string]string{
				"id":          id,
				"tags":        tags,
				"framework":   "host",
				"application": host,
				"namespace":   namespace,
				"container":   "",
				"resource_id": resourceId,
			}
		}

	}

	if len(foundResources) > 0 {
		now := time.Now().UTC()
		for _, details := range foundResources {
			_, err := dbms.Exec(`update cloud_resourses set updated_at = $1, tags = $2, last_seen = $3, is_active = true, status = 'Active' where id = $4`, now, details["tags"], now, details["id"])
			if err != nil {
				slog.Error("discovery: external resource update failed", "error", err)
				continue
			}
			err = insertOrUpdateResourceDiscovery(ctx, dbms, tenantId, accountId, details["id"], frameworkDiscovery{
				applicationType: details["framework"],
				namespace:       namespace,
				application:     details["application"],
				container:       "",
				dashboards: []string{
					details["framework"],
				},
			})
			if err != nil {
				ctx.GetLogger().Error("discovery: unable to insert resource discovery", "error", err)
			}
		}
	}

	for resourceId, id := range resourceMap {
		if _, ok := foundResources[resourceId]; !ok {
			_, err := dbms.Exec(`update cloud_resourses set is_active = false, status = 'Inactive', last_seen = $1 where id = $2`, time.Now().UTC(), id)
			if err != nil {
				slog.Error("discovery: external resource disable failed", "error", err)
				continue
			}
		}
	}

	return nil
}

func discoverAndUpdateExternalApps(ctx *security.RequestContext, tenantId, accountId string) error {
	endDate := time.Now().UTC()
	startDate := endDate.Add(time.Minute * -30)
	externalAppMap, err := relay.ExecutePrometheus(accountId, startDate, endDate, map[string]string{
		"postgres":   `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_postgres_queries_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
		"mysql":      `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_mysql_queries_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
		"mongodb":    `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_mongo_queries_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
		"clickhouse": `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_clickhouse_queries_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
		"cassandra":  `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_cassandra_queries_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
		"zookeeper":  `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_zookeeper_requests_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
		"redis":      `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_redis_queries_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
		"memcached":  `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_memcached_queries_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
		"rabbitmq":   `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_rabbitmq_messages_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
		"kafka":      `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_kafka_requests_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
		"nats":       `group by (destination, actual_destination_workload_name, actual_destination_workload_namespace) ({ __CLUSTER__ __name__="container_nats_messages_total", actual_destination_workload_namespace="external", actual_destination_workload_kind="external"})`,
	}, false)
	if err != nil {
		return err
	}
	exteralApps := map[string]string{}
	for appType, query := range externalAppMap {
		seriesListResult := query.(map[string]any)["series_list_result"]
		if seriesListResult != nil {
			for _, seriesAny := range seriesListResult.([]any) {
				series, ok := seriesAny.(map[string]any)
				if !ok {
					continue
				}
				metrixAny := series["metric"]
				if metrixAny == nil {
					continue
				}
				destinationWorkloadName := metrixAny.(map[string]any)["actual_destination_workload_name"]
				if destinationWorkloadName == nil {
					continue
				}
				destination := metrixAny.(map[string]any)["destination"]
				if destination == nil {
					continue
				}
				exteralApps[destination.(string)] = appType
			}
		}
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	resourceMap := map[string]string{}

	resourceRows, err := dbms.Query("select id::text, resourse_id from cloud_resourses where account = $1 and type = $2", accountId, "External")
	if err != nil {
		ctx.GetLogger().Error("discovery: unable to list external resources", "error", err, "tenant", tenantId, "account", accountId)
		return err
	}
	defer func() {
		err := resourceRows.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing resourceRows", "error", err)
		}
	}()

	for resourceRows.Next() {
		var id, resourceId string
		err = resourceRows.Scan(&id, &resourceId)
		if err != nil {
			ctx.GetLogger().Error("discovery: unable to scan row for external resources", "error", err, "tenant", tenantId, "account", accountId)
			return err
		}
		resourceMap[resourceId] = id
	}

	foundResources := map[string]map[string]string{}

	namespace := "external"
	resourceType := "External"
	for destination, appType := range exteralApps {
		resourceId := fmt.Sprintf("%s/%s/%s", namespace, resourceType, strings.ReplaceAll(strings.ReplaceAll(destination, ":", "-"), ".", "-"))
		id := resourceMap[resourceId]
		tags := fmt.Sprintf(`{"framework":"%s"}`, appType)

		if id == "" {
			id = uuid.NewString()
			now := time.Now().UTC()
			_, err := dbms.Exec(`INSERT INTO public.cloud_resourses
					(id, created_at, updated_at, resourse_id, "name", "type", status, account, cloud_provider, region, arn, tenant, tags, meta, service_name, first_seen, last_seen, is_active, external_resource_id)
					VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`, id, now, now, resourceId, destination, resourceType, "Active", accountId, "K8s", "global", "k8s://"+resourceId, tenantId, tags, "{}", appType, now, now, true, resourceId)
			if err != nil {
				slog.Error("discovery: external resource update failed", "error", err)
				continue
			}
			err = insertOrUpdateResourceDiscovery(ctx, dbms, tenantId, accountId, id, frameworkDiscovery{
				applicationType: appType,
				namespace:       namespace,
				application:     destination,
				container:       "",
				dashboards: []string{
					appType,
				},
			})
			if err != nil {
				ctx.GetLogger().Error("discovery: unable to insert resource discovery", "error", err)
			}
		} else {
			foundResources[resourceId] = map[string]string{
				"id":          id,
				"tags":        tags,
				"framework":   appType,
				"application": destination,
				"namespace":   namespace,
				"container":   "",
				"resource_id": resourceId,
			}
		}

	}

	if len(foundResources) > 0 {
		now := time.Now().UTC()
		for _, details := range foundResources {
			_, err := dbms.Exec(`update cloud_resourses set updated_at = $1, tags = $2, last_seen = $3, is_active = true, status = 'Active' where id = $4`, now, fmt.Sprintf(`{"framework":"%s"}`, details["framework"]), now, details["id"])
			if err != nil {
				slog.Error("discovery: external resource update failed", "error", err)
				continue
			}
			err = insertOrUpdateResourceDiscovery(ctx, dbms, tenantId, accountId, details["id"], frameworkDiscovery{
				applicationType: details["framework"],
				namespace:       namespace,
				application:     details["application"],
				container:       "",
				dashboards: []string{
					details["framework"],
				},
			})
			if err != nil {
				ctx.GetLogger().Error("discovery: unable to insert resource discovery", "error", err)
			}
		}
	}

	for resourceId, id := range resourceMap {
		if _, ok := foundResources[resourceId]; !ok {
			_, err := dbms.Exec(`update cloud_resourses set is_active = false, status = 'Inactive', last_seen = $1 where id = $2`, time.Now().UTC(), id)
			if err != nil {
				slog.Error("discovery: external resource disable failed", "error", err)
				continue
			}
		}
	}

	return nil
}

func discoverAndUpdateRelationships(ctx *security.RequestContext, tenantId, accountId string) error {
	// group by (destination, destination_workload_namespace, destination_workload_name, src_workload_namespace, src_workload_name) ({__name__ =~ ".+", destination_workload_namespace =~".+", destination_workload_name=~".+", src_workload_namespace =~".+", src_workload_name=~".+"})
	return nil
}

type frameworkDiscovery struct {
	namespace       string
	application     string
	applicationType string
	container       string
	dashboards      []string
}

type InvalidK8sOrdinalError struct {
	Value string
}

func (e *InvalidK8sOrdinalError) Error() string {
	return "invalid ordinal value: " + e.Value
}

func newInvalidK8sOrdinalError(value string) *InvalidK8sOrdinalError {
	return &InvalidK8sOrdinalError{
		Value: value,
	}
}

// TODO improve based on existing db rather than adhoc logic
func applicationNameFromPodName(namespaceName, podName string, cache map[string]string) string {

	if cache[fmt.Sprintf("%s/%s", namespaceName, podName)] != "" {
		return cache[fmt.Sprintf("%s/%s", namespaceName, podName)]
	}

	// Check for StatefulSet pattern: <app_name>-<ordinal>
	parts := strings.Split(podName, "-")
	if len(parts) > 1 {
		if _, err := parseK8sOrdinal(parts[len(parts)-1]); err == nil {
			return strings.Join(parts[:len(parts)-1], "-")
		}
	}

	// Check for Deployment pattern: <app_name>-<replica_set_hash>-<random_string>
	// Example: nginx-7896994969-m5g7c, my-app-6c57d8c464-txp74
	if len(parts) >= 3 {
		if isValidK8sHash(parts[len(parts)-2]) {
			return strings.Join(parts[0:len(parts)-2], "-")
		}
	}

	return podName
}

// parseK8sOrdinal checks if the given string is a valid integer (ordinal for StatefulSets).
func parseK8sOrdinal(s string) (int, error) {
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, newInvalidK8sOrdinalError(s)
		}
	}
	return 0, nil
}

// isValidK8sHash checks if the given string is a likely hexadecimal hash.
func isValidK8sHash(s string) bool {
	if len(s) < 5 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func discoverAndUpdateFrameworkAndDashboardAttributes(ctx *security.RequestContext, tenantId, accountId string) error {
	endDate := time.Now().UTC()
	startDate := endDate.Add(time.Minute * -30)
	dataMap, err := relay.ExecutePrometheus(accountId, startDate, endDate, map[string]string{
		"container_application_type": `group by (application_type, container_id) ({ __CLUSTER__  __name__="container_application_type"})`,
		"jvm_dashboard_otel":         `group by (namespace, pod, container) ({ __CLUSTER__  __name__=~"process.runtime.jvm.memory.usage|process_runtime_jvm_memory_usage_bytes"})`,
		"jvm_dashboard_nb":           `group by (container_id) ({ __CLUSTER__ __name__=~"container_jvm_heap_size_bytes"})`,
		"python_dashboard_otel":      `group by (namespace, pod, container) ({ __CLUSTER__  __name__=~"process.runtime.cpython.memory|process_runtime_cpython_memory_bytes"})`,
		"python_dashboard_nb":        `group by (container_id) ({ __CLUSTER__  __name__=~"container_python_thread_lock_wait_time_seconds"})`,
		"go_dashboard_otel":          `group by (namespace, pod, container) ({ __CLUSTER__ __name__=~"process.runtime.go.mem.heap_sys|process_runtime_go_mem_heap_sys_bytes|go.memory.used|go_memory_used_bytes"})`,
	}, false)

	if err != nil {
		ctx.GetLogger().Error("discovery: unable to unmarshal data", "error", err)
		return err
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	podWorkloadMap := map[string]string{}

	podRows, err := dbms.Query("select namespace, workload_name, name from k8s_pods where cloud_account_id = $1 and is_active = true", accountId)
	if err != nil {
		return err
	}
	defer func() {
		err := podRows.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing podRows", "error", err)
		}
	}()
	for podRows.Next() {
		var namespace, workload_name, name *string
		err = podRows.Scan(&namespace, &workload_name, &name)
		if err != nil {
			slog.Error("dicovery: unable to scan row for k8s_pods", "error", err)
			return err
		}
		if namespace == nil || workload_name == nil || name == nil {
			// happens when pods are launched without parent
			slog.Debug("dicovery: k8s_pods, found nil value", "namespace", namespace, "workload_name", workload_name, "name", name)
			continue
		}

		podWorkloadMap[fmt.Sprintf("%s/%s", *namespace, *name)] = *workload_name
	}

	appArnDataMap := map[string]frameworkDiscovery{}

	if dataMap["container_application_type"] != nil {
		seriesListResult := dataMap["container_application_type"].(map[string]any)["series_list_result"]
		if seriesListResult != nil {
			for _, seriesAny := range seriesListResult.([]any) {
				series, ok := seriesAny.(map[string]any)
				if !ok {
					continue
				}
				metrixAny := series["metric"]
				if metrixAny == nil {
					continue
				}
				metrix, ok := metrixAny.(map[string]any)
				if !ok {
					continue
				}

				containerIdAny := metrix["container_id"]
				if containerIdAny == nil {
					continue
				}

				applicationType := metrix["application_type"]
				if applicationType == nil {
					continue
				}

				containerId := containerIdAny.(string)
				containerIdSplits := strings.Split(containerId, "/")
				if len(containerIdSplits) != 5 {
					ctx.GetLogger().Error("discovery: invalid container id", "container_id", containerId)
					continue
				}

				discovery := frameworkDiscovery{
					namespace:       containerIdSplits[2],
					application:     applicationNameFromPodName(containerIdSplits[2], containerIdSplits[3], podWorkloadMap),
					container:       containerIdSplits[4],
					applicationType: applicationType.(string),
				}

				switch applicationType {
				case "nginx":
					discovery.dashboards = []string{"nginx"}
				case "rabbitmq":
					discovery.dashboards = []string{"rabbitmq"}
				case "redis":
					discovery.dashboards = []string{"redis"}
				case "kafka":
					discovery.dashboards = []string{"kafka"}
				case "elasticsearch":
					discovery.dashboards = []string{"elasticsearch"}
				case "mysql":
					discovery.dashboards = []string{"mysql"}
				case "postgresql":
					discovery.dashboards = []string{"postgresql"}
				case "mongodb":
					discovery.dashboards = []string{"mongodb"}
				case "cassandra":
					discovery.dashboards = []string{"cassandra"}
				case "memcached":
					discovery.dashboards = []string{"memcached"}
				case "zookeeper":
					discovery.dashboards = []string{"zookeeper"}
				case "etcd":
					discovery.dashboards = []string{"etcd"}
				case "clickhouse":
					discovery.dashboards = []string{"clickhouse"}
				case "nodejs":
					discovery.dashboards = []string{"nodejs"}
				}

				appArnDataMap[containerIdAny.(string)] = discovery

			}

		}

	}
	if dataMap["jvm_dashboard_otel"] != nil {
		series_list_result := dataMap["jvm_dashboard_otel"].(map[string]any)["series_list_result"]
		if series_list_result != nil {
			for _, seriesAny := range series_list_result.([]any) {
				series, ok := seriesAny.(map[string]any)
				if !ok {
					continue
				}
				metrixAny := series["metric"]
				if metrixAny == nil {
					continue
				}
				metrix, ok := metrixAny.(map[string]any)
				if !ok {
					continue
				}

				container := metrix["container"]
				if container == nil {
					continue
				}

				namespace := metrix["namespace"]
				if namespace == nil {
					continue
				}

				pod := metrix["pod"]
				if pod == nil {
					continue
				}

				arn := "/k8s/" + namespace.(string) + "/" + pod.(string) + "/" + container.(string)

				discovery := appArnDataMap[arn]
				if discovery.namespace == "" {
					discovery = frameworkDiscovery{
						namespace:       namespace.(string),
						application:     applicationNameFromPodName(namespace.(string), pod.(string), podWorkloadMap),
						container:       container.(string),
						applicationType: "java",
					}
				}
				discovery.dashboards = append(discovery.dashboards, "otel-jvm")
				appArnDataMap[arn] = discovery
			}
		}
	}
	if dataMap["jvm_dashboard_nb"] != nil {
		series_list_result := dataMap["jvm_dashboard_nb"].(map[string]any)["series_list_result"]
		if series_list_result != nil {
			for _, seriesAny := range series_list_result.([]any) {
				series, ok := seriesAny.(map[string]any)
				if !ok {
					continue
				}
				metrixAny := series["metric"]
				if metrixAny == nil {
					continue
				}
				metrix, ok := metrixAny.(map[string]any)
				if !ok {
					continue
				}

				containerIdAny := metrix["container_id"]
				if containerIdAny == nil {
					continue
				}

				containerId := containerIdAny.(string)
				containerIdSplits := strings.Split(containerId, "/")
				if len(containerIdSplits) != 5 {
					ctx.GetLogger().Error("discovery: invalid container id", "container_id", containerId)
					continue
				}

				discovery := appArnDataMap[containerIdAny.(string)]
				if discovery.namespace == "" {
					discovery = frameworkDiscovery{
						namespace:       containerIdSplits[2],
						application:     applicationNameFromPodName(containerIdSplits[2], containerIdSplits[3], podWorkloadMap),
						container:       containerIdSplits[4],
						applicationType: "java",
					}
				}
				discovery.dashboards = append(discovery.dashboards, "nb-jvm")
				appArnDataMap[containerId] = discovery
			}
		}
	}

	if dataMap["python_dashboard_otel"] != nil {
		series_list_result := dataMap["python_dashboard_otel"].(map[string]any)["series_list_result"]
		if series_list_result != nil {
			for _, seriesAny := range series_list_result.([]any) {
				series, ok := seriesAny.(map[string]any)
				if !ok {
					continue
				}
				metrixAny := series["metric"]
				if metrixAny == nil {
					continue
				}
				metrix, ok := metrixAny.(map[string]any)
				if !ok {
					continue
				}

				container := metrix["container"]
				if container == nil {
					continue
				}

				namespace := metrix["namespace"]
				if namespace == nil {
					continue
				}

				pod := metrix["pod"]
				if pod == nil {
					continue
				}

				arn := "/k8s/" + namespace.(string) + "/" + pod.(string) + "/" + container.(string)

				discovery := appArnDataMap[arn]
				if discovery.namespace == "" {
					discovery = frameworkDiscovery{
						namespace:       namespace.(string),
						application:     applicationNameFromPodName(namespace.(string), pod.(string), podWorkloadMap),
						container:       container.(string),
						applicationType: "python",
					}
				}
				discovery.dashboards = append(discovery.dashboards, "otel-python")
				appArnDataMap[arn] = discovery
			}
		}
	}
	if dataMap["python_dashboard_nb"] != nil {
		series_list_result := dataMap["python_dashboard_nb"].(map[string]any)["series_list_result"]
		if series_list_result != nil {
			for _, seriesAny := range series_list_result.([]any) {
				series, ok := seriesAny.(map[string]any)
				if !ok {
					continue
				}
				metrixAny := series["metric"]
				if metrixAny == nil {
					continue
				}
				metrix, ok := metrixAny.(map[string]any)
				if !ok {
					continue
				}

				containerIdAny := metrix["container_id"]
				if containerIdAny == nil {
					continue
				}

				containerId := containerIdAny.(string)
				containerIdSplits := strings.Split(containerId, "/")
				if len(containerIdSplits) != 5 {
					ctx.GetLogger().Error("discovery: invalid container id", "container_id", containerId)
					continue
				}

				discovery := appArnDataMap[containerIdAny.(string)]
				if discovery.namespace == "" {
					discovery = frameworkDiscovery{
						namespace:       containerIdSplits[2],
						application:     applicationNameFromPodName(containerIdSplits[2], containerIdSplits[3], podWorkloadMap),
						container:       containerIdSplits[4],
						applicationType: "python",
					}
				}
				discovery.dashboards = append(discovery.dashboards, "nb-python")
				appArnDataMap[containerId] = discovery
			}
		}
	}

	if dataMap["go_dashboard_otel"] != nil {
		series_list_result := dataMap["go_dashboard_otel"].(map[string]any)["series_list_result"]
		if series_list_result != nil {
			for _, seriesAny := range series_list_result.([]any) {
				series, ok := seriesAny.(map[string]any)
				if !ok {
					continue
				}
				metrixAny := series["metric"]
				if metrixAny == nil {
					continue
				}
				metrix, ok := metrixAny.(map[string]any)
				if !ok {
					continue
				}

				container := metrix["container"]
				if container == nil {
					continue
				}

				namespace := metrix["namespace"]
				if namespace == nil {
					continue
				}

				pod := metrix["pod"]
				if pod == nil {
					continue
				}

				arn := "/k8s/" + namespace.(string) + "/" + pod.(string) + "/" + container.(string)

				discovery := appArnDataMap[arn]
				if discovery.namespace == "" {
					discovery = frameworkDiscovery{
						namespace:       namespace.(string),
						application:     applicationNameFromPodName(namespace.(string), pod.(string), podWorkloadMap),
						container:       container.(string),
						applicationType: "golang",
					}
				}
				discovery.dashboards = append(discovery.dashboards, "otel-go")
				appArnDataMap[arn] = discovery
			}
		}
	}

	workloadIdMap := map[string]string{}

	workloadsRows, err := dbms.Query("select name, namespace, cloud_resource_id::text from k8s_workloads where cloud_account_id = $1 and is_active = true", accountId)
	if err != nil {
		return err
	}
	defer func() {
		err := workloadsRows.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing workloadsRows", "error", err)
		}
	}()
	for workloadsRows.Next() {
		var name, namespace, resourceId string
		err = workloadsRows.Scan(&name, &namespace, &resourceId)
		if err != nil {
			return err
		}
		workloadIdMap[fmt.Sprintf("%s/%s", namespace, name)] = resourceId
	}

	for arn, discovery := range appArnDataMap {
		if discovery.application == "" {
			ctx.GetLogger().Error("discovery: unable to identify application", "arn", arn, "data", slog.AnyValue(discovery))
			continue
		}
		workloadResourceId, ok := workloadIdMap[fmt.Sprintf("%s/%s", discovery.namespace, discovery.application)]
		if !ok {
			ctx.GetLogger().Error("discovery: unable to identify workload", "arn", arn, "data", slog.AnyValue(discovery))
			continue
		}

		err = insertOrUpdateResourceDiscovery(ctx, dbms, tenantId, accountId, workloadResourceId, discovery)
		if err != nil {
			ctx.GetLogger().Error("discovery: unable to insert resource discovery", "error", err, "arn", arn, "data", slog.AnyValue(discovery))
			continue
		}
	}
	return nil
}

func insertOrUpdateResourceDiscovery(ctx *security.RequestContext, dbms *database.DatabaseManager, tenantId, accountId, resourceId string, discovery frameworkDiscovery) error {
	frameworkKey := "framework"
	if discovery.container != "" {
		frameworkKey = "framework/" + discovery.container
	}
	err := insertOrUpdateCloudResourceAttribute(ctx, dbms, tenantId, accountId, resourceId, frameworkKey, discovery.applicationType)
	if err != nil {
		ctx.GetLogger().Error("discovery: unable to insert framework attribute", "error", err, "discovery", slog.AnyValue(discovery))
		return err
	}

	dashboardKey := "dashboard"
	if discovery.container != "" {
		dashboardKey = "dashboard/" + discovery.container
	}

	if len(discovery.dashboards) > 0 {
		err = insertOrUpdateCloudResourceAttribute(ctx, dbms, tenantId, accountId, resourceId, dashboardKey, strings.Join(discovery.dashboards, ","))
		if err != nil {
			ctx.GetLogger().Error("discovery: unable to insert dashboard attribute", "error", err, "discovery", slog.AnyValue(discovery))
			return err
		}
	}
	return nil
}

func insertOrUpdateCloudResourceAttribute(ctx *security.RequestContext, dbms *database.DatabaseManager, tenantId, accountId, resourceId, name, value string) error {
	_, err := dbms.Exec(`insert into cloud_resource_attributes 
	(tenant_id, account_id, resource_id, name, value) 
	values ($1, $2, $3, $4, $5)
	on conflict(resource_id, name) do update set value = excluded.value, last_seen_at = now()`, tenantId, accountId, resourceId, name, value)

	return err
}
