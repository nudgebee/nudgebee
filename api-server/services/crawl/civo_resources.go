package crawl

import (
	_ "embed"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

type PricingData struct {
	Name    string `json:"name"`
	Age     int    `json:"age"`
	Address string `json:"address"`
}

type NodePricingData struct {
	Name         string  `json:"name"`
	Size         string  `json:"size"`
	RAM          string  `json:"ram"`
	CPU          string  `json:"cpu"`
	Storage      string  `json:"storage"`
	DataTransfer string  `json:"data_transfer"`
	PricePerHour float64 `json:"price_per_hour"`
}

func extractCPU(cpu string) (int, error) {
	var cores int
	_, err := fmt.Sscanf(cpu, "%d", &cores)
	if err != nil {
		return 0, err
	}
	return cores, nil
}

//go:embed civo_pricing.json

var civo_instance_data []byte

func getJsonData() ([]NodePricingData, error) {
	var nodes []NodePricingData
	err := common.UnmarshalJson(civo_instance_data, &nodes)
	if err != nil {
		fmt.Println("Error decoding JSON:", err)
		return nil, err
	}
	return nodes, err
}

func processCivoNodeInfo(ctx *security.RequestContext) ([]*InstanceInfo, error) {
	nodes, err := getJsonData()
	if err != nil {
		ctx.GetLogger().Error("Error loading nodes pricing data", "error", err)
		return nil, err
	}

	var memoryCapacity int
	var cpuCapacity int

	var instanceInfoList []*InstanceInfo
	for _, node := range nodes {
		if _, err := fmt.Sscanf(node.RAM, "%d", &memoryCapacity); err != nil {
			ctx.GetLogger().Error("Error parsing RAM capacity", "error", err)
			return nil, err
		}
		cpuCapacity, err = extractCPU(node.CPU)
		if err != nil {
			ctx.GetLogger().Error("Error parsing CPU capacity", "error", err)
			return nil, err
		}
		instanceInfoList = append(instanceInfoList, &InstanceInfo{
			CloudProvider:     "civo",
			ServiceName:       "civo",
			ServiceType:       "Compute",
			ResourceType:      node.Name,
			ResourceRegion:    "nyc1",
			ResourceCost:      node.PricePerHour,
			ResourceCapacity:  safeJsonDump(map[string]any{"memory_gb": memoryCapacity, "cpu_virtual": cpuCapacity}),
			Attributes:        "{}",
			Architecture:      "x86_64",
			OperatingSystem:   "Linux",
			CurrentGeneration: true,
			PriceUnit:         "hourly",
			PricingModel:      "on_demand",
		})
	}

	return instanceInfoList, nil
}

func CivoResourceMeta(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	civoNodesData, err := processCivoNodeInfo(ctx)
	if err != nil {
		return err
	}

	ctx.GetLogger().Info("Inserting Civo resource details into database", "instanceInfo", len(civoNodesData))
	for i := 0; i < len(civoNodesData); i += 50 {
		end := i + 50
		if end > len(civoNodesData) {
			end = len(civoNodesData)
		}
		err = insertInstanceInfo(dbms, civoNodesData[i:end])
		if err != nil {
			ctx.GetLogger().Error("Error inserting instance info", "error", err)
			return err
		}
	}
	return nil
}
