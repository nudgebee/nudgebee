package flow_sources

import (
	"strings"
)

// K8sDNSParser parses Kubernetes internal DNS names
type K8sDNSParser struct{}

// NewK8sDNSParser creates a new K8s DNS parser
func NewK8sDNSParser() *K8sDNSParser {
	return &K8sDNSParser{}
}

// K8sServiceInfo holds parsed K8s service information
type K8sServiceInfo struct {
	ServiceName string
	Namespace   string
	IsValid     bool
}

// Parse parses a K8s service DNS name and returns service info
// Handles:
//   - service.namespace.svc.cluster.local -> (service, namespace)
//   - service.namespace.svc -> (service, namespace)
//   - service.namespace -> (service, namespace)
//   - service -> (service, "")
func (p *K8sDNSParser) Parse(name string) K8sServiceInfo {
	if name == "" {
		return K8sServiceInfo{IsValid: false}
	}

	// Remove K8s DNS suffixes
	cleanName := name
	cleanName = strings.TrimSuffix(cleanName, ".svc.cluster.local")
	cleanName = strings.TrimSuffix(cleanName, ".svc")
	cleanName = strings.TrimSuffix(cleanName, ".pod.cluster.local")

	if cleanName == "" {
		return K8sServiceInfo{IsValid: false}
	}

	parts := strings.Split(cleanName, ".")

	// Validate first part (service name) is not empty
	if len(parts) == 0 || parts[0] == "" {
		return K8sServiceInfo{IsValid: false}
	}

	// Single part: just service name
	if len(parts) == 1 {
		return K8sServiceInfo{
			ServiceName: parts[0],
			Namespace:   "",
			IsValid:     true,
		}
	}

	// Two or more parts: service.namespace (ignore additional parts)
	return K8sServiceInfo{
		ServiceName: parts[0],
		Namespace:   parts[1],
		IsValid:     true,
	}
}

// IsK8sInternalDNS checks if a hostname is a Kubernetes internal DNS name
func (p *K8sDNSParser) IsK8sInternalDNS(hostname string) bool {
	if hostname == "" {
		return false
	}
	return strings.HasSuffix(hostname, ".svc.cluster.local") ||
		strings.HasSuffix(hostname, ".svc") ||
		strings.HasSuffix(hostname, ".pod.cluster.local") ||
		hostname == "localhost" ||
		hostname == "127.0.0.1"
}

// LooksLikeK8sServiceName checks if a name looks like it could be a K8s service name
// Returns true only for patterns that strongly suggest K8s internal DNS
func (p *K8sDNSParser) LooksLikeK8sServiceName(name string) bool {
	// Skip if it's clearly an external hostname
	externalTLDs := []string{".com", ".io", ".org", ".net", ".edu", ".gov", ".co", ".app"}
	for _, tld := range externalTLDs {
		if strings.Contains(name, tld) {
			return false
		}
	}

	// Skip AWS hostnames
	if strings.Contains(name, "amazonaws") || strings.Contains(name, "cloudfront") {
		return false
	}

	// K8s service names are typically lowercase with hyphens
	nameLower := strings.ToLower(name)
	if name != nameLower {
		return false
	}

	// Must have at least one dot to look like K8s DNS (service.namespace pattern)
	// Single word names like "redis" are ambiguous
	if !strings.Contains(name, ".") {
		return false
	}

	// Check for common K8s service patterns
	return !strings.Contains(name, "_") &&
		len(name) > 0 &&
		len(name) < 64 // K8s name length limit
}

// CleanServiceName removes protocol prefix and port from a service name
func (p *K8sDNSParser) CleanServiceName(name string) string {
	// Remove protocol prefix
	name = strings.TrimPrefix(name, "http://")
	name = strings.TrimPrefix(name, "https://")
	name = strings.TrimPrefix(name, "grpc://")
	name = strings.TrimPrefix(name, "tcp://")

	// Remove port
	if idx := strings.LastIndex(name, ":"); idx != -1 {
		// Make sure it's a port (digits after colon)
		port := name[idx+1:]
		isPort := true
		for _, c := range port {
			if c < '0' || c > '9' {
				isPort = false
				break
			}
		}
		if isPort && len(port) > 0 {
			name = name[:idx]
		}
	}

	// Remove trailing path
	if idx := strings.Index(name, "/"); idx != -1 {
		name = name[:idx]
	}

	return name
}
