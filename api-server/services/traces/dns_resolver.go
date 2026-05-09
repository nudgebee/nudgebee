package traces

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"nudgebee/services/common"
	"strings"
	"sync"
	"time"
)

func init() {
	InitDNSCache()
}

// DNSResolutionInfo holds DNS resolution results for a service
type DNSResolutionInfo struct {
	Hostname    string   `json:"hostname"`
	CNAME       string   `json:"cname,omitempty"`        // Canonical name (e.g., AWS endpoint)
	IPs         []string `json:"ips,omitempty"`          // Resolved IP addresses
	ResolvedAt  string   `json:"resolved_at,omitempty"`  // When resolution was performed
	CloudVendor string   `json:"cloud_vendor,omitempty"` // Detected cloud vendor (aws, gcp, azure)
	ServiceType string   `json:"service_type,omitempty"` // Detected service type (elasticsearch, rds, etc.)
}

// InitDNSCache initializes the DNS resolution cache namespace
func InitDNSCache() {
	// Cache DNS resolutions for 1 hour (DNS records are relatively stable)
	common.CacheCreateNamespace("dns_resolution",
		common.CacheNamespaceWithExpiration(1*time.Hour),
		common.CacheNamespaceWithMaxEntries(10000),
	)
}

// ResolveDNS performs DNS resolution for a hostname with caching
func (t *TraceServiceMapBuilder) ResolveDNS(hostname string) *DNSResolutionInfo {
	if hostname == "" {
		return nil
	}

	// Skip if it's already an IP address
	if net.ParseIP(hostname) != nil {
		return &DNSResolutionInfo{
			Hostname: hostname,
			IPs:      []string{hostname},
		}
	}

	// Check cache first
	if cached, found := common.CacheGet("dns_resolution", hostname); found {
		var info DNSResolutionInfo
		if err := json.Unmarshal(cached, &info); err == nil {
			return &info
		}
	}

	// Perform DNS resolution
	info := &DNSResolutionInfo{
		Hostname:   hostname,
		ResolvedAt: time.Now().Format(time.RFC3339),
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Lookup CNAME
	cname, err := net.DefaultResolver.LookupCNAME(ctx, hostname)
	if err == nil && cname != "" && cname != hostname+"." {
		info.CNAME = strings.TrimSuffix(cname, ".")

		// Detect cloud vendor and service type from CNAME
		t.detectCloudServiceFromCNAME(info)
	}

	// Lookup IP addresses
	ips, err := net.DefaultResolver.LookupHost(ctx, hostname)
	if err == nil && len(ips) > 0 {
		info.IPs = ips
	}

	// Cache the result (even if resolution failed, to avoid repeated lookups)
	if data, err := json.Marshal(info); err == nil {
		if err := common.CacheSet("dns_resolution", hostname, data); err != nil {
			slog.Warn("failed to cache DNS resolution", "hostname", hostname, "error", err)
		}
	}

	return info
}

// detectCloudServiceFromCNAME detects cloud vendor and service type from CNAME
func (t *TraceServiceMapBuilder) detectCloudServiceFromCNAME(info *DNSResolutionInfo) {
	if info.CNAME == "" {
		return
	}

	cname := strings.ToLower(info.CNAME)

	// AWS Services
	if strings.Contains(cname, ".amazonaws.com") || strings.Contains(cname, ".aws") {
		info.CloudVendor = "aws"

		// AWS Elasticsearch/OpenSearch
		if strings.Contains(cname, ".es.amazonaws.com") {
			info.ServiceType = "aws-elasticsearch"
		} else if strings.Contains(cname, ".aoss.amazonaws.com") {
			info.ServiceType = "aws-opensearch-serverless"
		} else if strings.Contains(cname, "rds.amazonaws.com") {
			info.ServiceType = "aws-rds"
		} else if strings.Contains(cname, "elasticache.amazonaws.com") {
			info.ServiceType = "aws-elasticache"
		} else if strings.Contains(cname, "elb.amazonaws.com") {
			info.ServiceType = "aws-elb"
		} else if strings.Contains(cname, ".eks.amazonaws.com") {
			info.ServiceType = "aws-eks"
		} else if strings.Contains(cname, ".s3.amazonaws.com") || strings.Contains(cname, ".s3-") {
			info.ServiceType = "aws-s3"
		} else if strings.Contains(cname, ".cloudfront.net") {
			info.ServiceType = "aws-cloudfront"
		} else if strings.Contains(cname, ".lambda.") {
			info.ServiceType = "aws-lambda"
		}
	}

	// GCP Services
	if strings.Contains(cname, ".googleapis.com") || strings.Contains(cname, ".gcp") {
		info.CloudVendor = "gcp"

		if strings.Contains(cname, "storage.googleapis.com") {
			info.ServiceType = "gcp-storage"
		} else if strings.Contains(cname, "firestore.googleapis.com") {
			info.ServiceType = "gcp-firestore"
		} else if strings.Contains(cname, "cloudsql") {
			info.ServiceType = "gcp-cloudsql"
		} else if strings.Contains(cname, "redis.googleapis.com") {
			info.ServiceType = "gcp-memorystore"
		}
	}

	// Azure Services
	if strings.Contains(cname, ".azure.com") || strings.Contains(cname, ".windows.net") {
		info.CloudVendor = "azure"

		if strings.Contains(cname, "database.windows.net") {
			info.ServiceType = "azure-sql-database"
		} else if strings.Contains(cname, "redis.cache.windows.net") {
			info.ServiceType = "azure-redis-cache"
		} else if strings.Contains(cname, "blob.core.windows.net") {
			info.ServiceType = "azure-blob-storage"
		} else if strings.Contains(cname, "cosmos.azure.com") {
			info.ServiceType = "azure-cosmos-db"
		}
	}
}

// ResolveDNSForExternalServices resolves DNS for all external services in the map
func (t *TraceServiceMapBuilder) ResolveDNSForExternalServices(externalServices map[string]*ExternalServiceInfo) map[string]*DNSResolutionInfo {
	dnsCache := make(map[string]*DNSResolutionInfo)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Limit concurrency to avoid file descriptor exhaustion or DNS throttling
	semaphore := make(chan struct{}, 20)

	for serviceName := range externalServices {
		// Optimization: Handle IPs synchronously as they don't need network
		if net.ParseIP(serviceName) != nil {
			if info := t.ResolveDNS(serviceName); info != nil {
				dnsCache[serviceName] = info
			}
			continue
		}

		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire token
			defer func() { <-semaphore }() // Release token

			// Perform DNS resolution (ResolveDNS handles caching internally)
			if info := t.ResolveDNS(name); info != nil {
				mu.Lock()
				dnsCache[name] = info
				mu.Unlock()
			}
		}(serviceName)
	}

	wg.Wait()
	return dnsCache
}
