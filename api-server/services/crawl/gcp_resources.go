package crawl

import (
	_ "embed"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"
)

type GCPInstanceInfo struct {
	InstanceType string  `json:"type"`
	Core         int     `json:"core"`
	Memory       string  `json:"memory"`
	Cost         float64 `json:"cost"`
	Region       string  `json:"region"`
}

//go:embed gcp_pricing.json
var gcp_instance_data []byte

func getGCPInstanceInfo() ([]GCPInstanceInfo, error) {
	var gcpInstanceInfo []GCPInstanceInfo
	err := common.UnmarshalJson(gcp_instance_data, &gcpInstanceInfo)
	if err != nil {
		return nil, err
	}
	return gcpInstanceInfo, nil
}

func GCPResourceMeta(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Broaden detection: match GKE provider or nodes with GKE labels
	region_rows, err := dbms.Db.Query(`
		SELECT DISTINCT ksn.meta -> 'node_info' -> 'labels' ->> 'topology.kubernetes.io/region'
		FROM k8s_nodes ksn
		WHERE ksn.is_active IS NOT FALSE
		  AND (
			EXISTS (SELECT 1 FROM agent a WHERE a.cloud_account_id = ksn.cloud_account_id AND a.k8s_provider = 'GKE')
			OR ksn.meta -> 'node_info' -> 'labels' ? 'cloud.google.com/gke-nodepool'
		  )
		  AND ksn.meta -> 'node_info' -> 'labels' ->> 'topology.kubernetes.io/region' IS NOT NULL
	`)

	if err != nil {
		return err
	}
	defer func() { _ = region_rows.Close() }()

	regions := make([]string, 0)
	for region_rows.Next() {
		var region string
		err = region_rows.Scan(&region)
		if err != nil {
			ctx.GetLogger().Error("Error scanning region", "error", err)
			return err
		}
		if region != "" {
			regions = append(regions, region)
		}
	}

	if len(regions) == 0 {
		ctx.GetLogger().Info("No GKE regions found, skipping GCP pricing crawl")
		return nil
	}

	gcp_instance_data, err := getGCPInstanceInfo()
	if err != nil {
		ctx.GetLogger().Error("Error getting gcp instance info", "error", err)
		return err
	}
	instanceInfo := make([]*InstanceInfo, 0)
	for _, instance := range gcp_instance_data {
		for _, region := range regions {
			arch := "x86_64"
			if strings.HasPrefix(instance.InstanceType, "t2a-") || strings.HasPrefix(instance.InstanceType, "c3a-") || strings.HasPrefix(instance.InstanceType, "c4a-") {
				arch = "arm64"
			}
			instanceInfo = append(instanceInfo, &InstanceInfo{
				CloudProvider:     "gcp",
				ServiceName:       "Compute",
				ServiceType:       "Compute",
				ResourceType:      instance.InstanceType,
				ResourceRegion:    region,
				ResourceCapacity:  safeJsonDump(map[string]any{"memory_gb": instance.Memory, "cpu_virtual": instance.Core}),
				ResourceCost:      instance.Cost,
				Attributes:        safeJsonDump(map[string]any{"instance_type": instance.InstanceType}),
				Architecture:      arch,
				OperatingSystem:   "Linux",
				CurrentGeneration: true,
				PriceUnit:         "hourly",
				PricingModel:      "on_demand",
			})
		}
	}
	ctx.GetLogger().Info("Inserting gcp resource details into database", "instanceInfo", len(instanceInfo))
	for i := 0; i < len(instanceInfo); i += 50 {
		end := i + 50
		if end > len(instanceInfo) {
			end = len(instanceInfo)
		}
		err = insertInstanceInfo(dbms, instanceInfo[i:end])
		if err != nil {
			ctx.GetLogger().Error("Error inserting instance info", "error", err)
			return err
		}
	}
	return nil
}
