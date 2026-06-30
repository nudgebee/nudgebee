package opencostengine

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/opencost/opencost/core/pkg/clustercache"
	coreenv "github.com/opencost/opencost/core/pkg/env"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/core/pkg/util"
	"github.com/opencost/opencost/pkg/cloud/models"
	"github.com/opencost/opencost/pkg/cloud/provider"
)

// Default cost ratios from GCP us-central1 E2 pricing.
const (
	defaultCPUCostPerHr = 0.021811
	defaultRAMCostPerHr = 0.002923
	defaultGPUCostPerHr = 0.95
	defaultStorageCost  = "0.00005479452"
)

// Spot label constants across cloud providers.
var spotLabels = map[string]string{
	"karpenter.sh/capacity-type":            "spot",
	"eks.amazonaws.com/capacityType":        "SPOT",
	"node.kubernetes.io/lifecycle":          "spot",
	"cloud.google.com/gke-spot":             "true",
	"kubernetes.azure.com/scalesetpriority": "spot",
}

// NudgebeeProvider implements models.Provider using cloud_resource_details DB table for pricing.
type NudgebeeProvider struct {
	db                      *DB
	clusterID               string
	Pricing                 map[string]*provider.NodePrice
	DownloadPricingDataLock sync.RWMutex
	ClusterRegion           string
	ClusterAccountID        string
}

func NewNudgebeeProvider(db *DB, clusterID string) (*NudgebeeProvider, error) {
	if clusterID == "" {
		return nil, fmt.Errorf("clusterID cannot be empty")
	}
	return &NudgebeeProvider{
		db:        db,
		clusterID: clusterID,
		Pricing:   make(map[string]*provider.NodePrice),
	}, nil
}

// DownloadPricingData loads pricing from cloud_resource_details table.
func (np *NudgebeeProvider) DownloadPricingData() error {
	np.DownloadPricingDataLock.Lock()
	defer np.DownloadPricingDataLock.Unlock()

	if np.clusterID == "" {
		return fmt.Errorf("clusterID cannot be empty")
	}

	// Get cloud provider name for this cluster
	var cloudProviderName string
	err := np.db.Connection().QueryRow(
		"SELECT cloud_provider FROM cloud_accounts WHERE id = $1 AND status = 'active'",
		np.clusterID,
	).Scan(&cloudProviderName)
	if err != nil {
		log.Warnf("NudgebeeProvider: could not get cloud provider for cluster %s: %s", np.clusterID, err)
		cloudProviderName = ""
	}
	cloudProviderName = strings.ToLower(cloudProviderName)

	// K8s clusters store "K8s" as provider; detect actual cloud from node labels
	if cloudProviderName == "k8s" {
		cloudProviderName = np.detectCloudFromNodes()
	}

	np.Pricing = make(map[string]*provider.NodePrice)

	// Set defaults
	dp := provider.DefaultPricing()
	np.Pricing["default"] = &provider.NodePrice{CPU: dp.CPU, RAM: dp.RAM}
	np.Pricing["default,spot"] = &provider.NodePrice{CPU: dp.SpotCPU, RAM: dp.SpotRAM}
	np.Pricing["default,gpu"] = &provider.NodePrice{CPU: dp.CPU, RAM: dp.RAM, GPU: dp.GPU}

	if cloudProviderName == "" {
		log.Infof("NudgebeeProvider: no cloud provider for cluster %s, using default pricing", np.clusterID)
		return nil
	}

	rows, err := np.db.Connection().Query(`
		SELECT resource_type, resource_region, resource_cost, resource_capacity,
		       pricing_model, gpu_count, spot_pricing
		FROM cloud_resource_details
		WHERE cloud_provider = $1
		  AND service_type IN ('Compute', 'compute')
		  AND resource_cost > 0
	`, cloudProviderName)
	if err != nil {
		log.Errorf("NudgebeeProvider: error querying pricing: %s", err)
		return nil // fall back to defaults
	}
	defer func() { _ = rows.Close() }()

	count := 0
	for rows.Next() {
		var (
			resourceType   string
			resourceRegion string
			resourceCost   float64
			capacityJSON   []byte
			pricingModel   string
			gpuCount       int
			spotPricing    []byte
		)
		if err := rows.Scan(&resourceType, &resourceRegion, &resourceCost, &capacityJSON, &pricingModel, &gpuCount, &spotPricing); err != nil {
			log.Warnf("NudgebeeProvider: error scanning row: %s", err)
			continue
		}

		cpuVirtual, memoryGB := parseCapacity(capacityJSON)
		if cpuVirtual == 0 || memoryGB == 0 {
			continue
		}

		cpuCost, ramCost := splitCost(resourceCost, cpuVirtual, memoryGB)

		key := fmt.Sprintf("%s,%s", resourceRegion, resourceType)
		if pricingModel == "spot" {
			key += ",spot"
		}

		np.Pricing[key] = &provider.NodePrice{
			CPU: fmt.Sprintf("%f", cpuCost),
			RAM: fmt.Sprintf("%f", ramCost),
		}

		// AWS stores spot pricing in spot_pricing JSONB column within on-demand rows.
		// Extract median spot price and create a spot pricing entry.
		if pricingModel != "spot" {
			if spotCost := extractMedianSpotPrice(spotPricing); spotCost > 0 {
				spotCPU, spotRAM := splitCost(spotCost, cpuVirtual, memoryGB)
				spotKey := key + ",spot"
				np.Pricing[spotKey] = &provider.NodePrice{
					CPU: fmt.Sprintf("%f", spotCPU),
					RAM: fmt.Sprintf("%f", spotRAM),
				}
			}
		}

		if gpuCount > 0 {
			gpuKey := key + ",gpu"
			np.Pricing[gpuKey] = &provider.NodePrice{
				CPU: fmt.Sprintf("%f", cpuCost),
				RAM: fmt.Sprintf("%f", ramCost),
				GPU: fmt.Sprintf("%f", defaultGPUCostPerHr),
			}
		}
		count++
	}
	if err := rows.Err(); err != nil {
		log.Errorf("NudgebeeProvider: error iterating pricing rows: %s", err)
	}

	log.Infof("NudgebeeProvider: loaded %d pricing entries for %s", count, cloudProviderName)
	return nil
}

// extractMedianSpotPrice returns the median spot price from the spot_pricing JSONB array.
// Format: [{"az": "us-east-1a", "price": 0.123}, ...]
func extractMedianSpotPrice(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}
	var entries []struct {
		Price float64 `json:"price"`
	}
	if err := json.Unmarshal(data, &entries); err != nil || len(entries) == 0 {
		return 0
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Price < entries[j].Price })
	return entries[len(entries)/2].Price
}

// parseCapacity extracts cpu_virtual and memory_gb from the resource_capacity jsonb.
// Handles both string and numeric types for cpu_virtual.
func parseCapacity(data []byte) (cpu, mem float64) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, 0
	}
	cpu = parseJSONNumber(raw["cpu_virtual"])
	mem = parseJSONNumber(raw["memory_gb"])
	return
}

func parseJSONNumber(raw json.RawMessage) float64 {
	if raw == nil {
		return 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		f, _ = strconv.ParseFloat(s, 64)
		return f
	}
	return 0
}

// detectCloudFromNodes checks k8s_nodes labels to determine the actual cloud provider.
func (np *NudgebeeProvider) detectCloudFromNodes() string {
	var labels []byte
	err := np.db.Connection().QueryRow(
		"SELECT labels FROM k8s_nodes WHERE cloud_account_id = $1 AND is_active = true LIMIT 1",
		np.clusterID,
	).Scan(&labels)
	if err != nil {
		return ""
	}
	labelsStr := string(labels)
	switch {
	case strings.Contains(labelsStr, "eks.amazonaws.com") || strings.Contains(labelsStr, "karpenter.sh"):
		return "aws"
	case strings.Contains(labelsStr, "cloud.google.com"):
		return "gcp"
	case strings.Contains(labelsStr, "kubernetes.azure.com"):
		return "azure"
	case strings.Contains(labelsStr, "civo.com"):
		return "civo"
	}
	return ""
}

// splitCost splits total hourly cost into per-vCPU and per-GiB costs using default ratios.
func splitCost(totalCost, cpuVirtual, memoryGB float64) (cpuPerVCPU, ramPerGiB float64) {
	cpuToRAMRatio := defaultCPUCostPerHr / defaultRAMCostPerHr
	ramMultiple := cpuVirtual*cpuToRAMRatio + memoryGB
	ramPerGiB = totalCost / ramMultiple
	cpuPerVCPU = ramPerGiB * cpuToRAMRatio
	return
}

func (np *NudgebeeProvider) GetKey(labels map[string]string, n *clustercache.Node) models.Key {
	instanceType, _ := util.GetInstanceType(labels)
	region, _ := util.GetRegion(labels)
	isSpot := isNodeSpot(labels)
	gpuCount := 0
	if n != nil {
		if gpuQty, ok := n.Status.Capacity["nvidia.com/gpu"]; ok {
			gpuCount = int(gpuQty.Value())
		}
	}
	return &nudgebeeKey{
		InstanceType: instanceType,
		Region:       region,
		IsSpot:       isSpot,
		GpuCount:     gpuCount,
		Labels:       labels,
	}
}

func isNodeSpot(labels map[string]string) bool {
	for label, spotValue := range spotLabels {
		if v, ok := labels[label]; ok && strings.EqualFold(v, spotValue) {
			return true
		}
	}
	return false
}

func (np *NudgebeeProvider) NodePricing(key models.Key) (*models.Node, models.PricingMetadata, error) {
	np.DownloadPricingDataLock.RLock()
	defer np.DownloadPricingDataLock.RUnlock()

	k := key.Features()
	pricing, ok := np.Pricing[k]
	if !ok {
		// Try without spot suffix
		k = strings.TrimSuffix(k, ",spot")
		pricing, ok = np.Pricing[k]
	}
	if !ok {
		pricing = np.Pricing["default"]
	}
	if pricing == nil {
		return &models.Node{VCPUCost: "0", RAMCost: "0", GPUCost: "0"}, models.PricingMetadata{}, nil
	}

	usageType := "ondemand"
	if nk, ok := key.(*nudgebeeKey); ok && nk.IsSpot {
		usageType = "spot"
	}

	gpuCost := pricing.GPU
	var gpuCount string
	if nk, ok := key.(*nudgebeeKey); ok && nk.GpuCount > 0 {
		gpuCount = strconv.Itoa(nk.GpuCount)
		if gpuCost == "" {
			gpuCost = fmt.Sprintf("%f", defaultGPUCostPerHr)
		}
	} else if key.GPUType() != "" {
		gpuCount = "1"
		if gpuCost == "" {
			gpuCost = fmt.Sprintf("%f", defaultGPUCostPerHr)
		}
	}

	return &models.Node{
		VCPUCost:  pricing.CPU,
		RAMCost:   pricing.RAM,
		GPUCost:   gpuCost,
		GPU:       gpuCount,
		UsageType: usageType,
	}, models.PricingMetadata{}, nil
}

func (np *NudgebeeProvider) AllNodePricing() (interface{}, error) {
	np.DownloadPricingDataLock.RLock()
	defer np.DownloadPricingDataLock.RUnlock()
	return np.Pricing, nil
}

func (np *NudgebeeProvider) GetConfig() (*models.CustomPricing, error) {
	return provider.DefaultPricing(), nil
}

func (np *NudgebeeProvider) ClusterInfo() (map[string]string, error) {
	m := make(map[string]string)
	m["provider"] = opencost.CustomProvider
	m["id"] = coreenv.GetClusterID()
	return m, nil
}

func (np *NudgebeeProvider) PVPricing(pvk models.PVKey) (*models.PV, error) {
	return &models.PV{Cost: defaultStorageCost}, nil
}

func (np *NudgebeeProvider) NetworkPricing() (*models.Network, error) {
	return &models.Network{
		ZoneNetworkEgressCost:     0.01,
		RegionNetworkEgressCost:   0.01,
		InternetNetworkEgressCost: 0.12,
	}, nil
}

func (np *NudgebeeProvider) LoadBalancerPricing() (*models.LoadBalancer, error) {
	return &models.LoadBalancer{Cost: 0.025}, nil
}

func (np *NudgebeeProvider) GetPVKey(pv *clustercache.PersistentVolume, parameters map[string]string, defaultRegion string) models.PVKey {
	return &nudgebeePVKey{Labels: pv.Labels, StorageClassName: pv.Spec.StorageClassName, DefaultRegion: defaultRegion}
}

func (np *NudgebeeProvider) PricingSourceSummary() interface{} { return np.Pricing }
func (np *NudgebeeProvider) GetAddresses() ([]byte, error)     { return nil, nil }
func (np *NudgebeeProvider) GetDisks() ([]byte, error)         { return nil, nil }
func (np *NudgebeeProvider) GetOrphanedResources() ([]models.OrphanedResource, error) {
	return nil, nil
}
func (np *NudgebeeProvider) GpuPricing(map[string]string) (string, error) { return "", nil }
func (np *NudgebeeProvider) UpdateConfig(r io.Reader, updateType string) (*models.CustomPricing, error) {
	return provider.DefaultPricing(), nil
}
func (np *NudgebeeProvider) UpdateConfigFromConfigMap(a map[string]string) (*models.CustomPricing, error) {
	return provider.DefaultPricing(), nil
}
func (np *NudgebeeProvider) GetManagementPlatform() (string, error)               { return "", nil }
func (np *NudgebeeProvider) ApplyReservedInstancePricing(map[string]*models.Node) {}
func (np *NudgebeeProvider) ServiceAccountStatus() *models.ServiceAccountStatus {
	return &models.ServiceAccountStatus{Checks: []*models.ServiceAccountCheck{}}
}
func (np *NudgebeeProvider) PricingSourceStatus() map[string]*models.PricingSource {
	return make(map[string]*models.PricingSource)
}
func (np *NudgebeeProvider) ClusterManagementPricing() (string, float64, error) { return "", 0.0, nil }
func (np *NudgebeeProvider) CombinedDiscountForNode(instanceType string, isPreemptible bool, defaultDiscount, negotiatedDiscount float64) float64 {
	return 1.0 - ((1.0 - defaultDiscount) * (1.0 - negotiatedDiscount))
}
func (np *NudgebeeProvider) Regions() []string { return []string{} }

// --- Key types ---

type nudgebeeKey struct {
	InstanceType string
	Region       string
	IsSpot       bool
	GpuCount     int
	Labels       map[string]string
}

func (k *nudgebeeKey) ID() string { return k.Labels["providerID"] }
func (k *nudgebeeKey) Features() string {
	base := fmt.Sprintf("%s,%s", k.Region, k.InstanceType)
	if k.IsSpot {
		base += ",spot"
	}
	return base
}
func (k *nudgebeeKey) GPUType() string {
	for _, label := range []string{"nvidia.com/gpu.product", "gpu.nvidia.com/class"} {
		if v, ok := k.Labels[label]; ok {
			return v
		}
	}
	return ""
}
func (k *nudgebeeKey) GPUCount() int {
	return k.GpuCount
}

type nudgebeePVKey struct {
	Labels           map[string]string
	StorageClassName string
	DefaultRegion    string
}

func (k *nudgebeePVKey) ID() string              { return "" }
func (k *nudgebeePVKey) GetStorageClass() string { return k.StorageClassName }
func (k *nudgebeePVKey) Features() string {
	region, ok := util.GetRegion(k.Labels)
	if !ok {
		region = k.DefaultRegion
	}
	return region + "," + k.StorageClassName
}
