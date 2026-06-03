package flow_sources

import "strings"

// EBPFTarget is the parsed form of a New Relic EBPFSERVER entity name.
//
// New Relic's eBPF integration synthesizes EBPFSERVER entities for downstream
// targets observed in OTel/eBPF traffic, with the entity name encoded as
// "<ip>/<port>/<fqdn>" — for example:
//
//	34.118.228.211/6379/argocd-redis.argocd.svc.cluster.local
//
// The FQDN is pre-resolved by NR's pipeline, which lets us match against
// existing K8s Service nodes by name without going through the cluster-IP
// resolver. parseEBPFServerName returns ok=false for any name that doesn't
// fit the three-segment shape; callers should skip those rows.
type EBPFTarget struct {
	IP   string
	Port string
	FQDN string
}

// parseEBPFServerName splits an EBPFSERVER entity name into (IP, Port, FQDN).
// Returns ok=false when the input does not have the expected three slash-
// separated segments. The function does not validate that IP is a valid IP or
// that FQDN is a valid hostname — the caller decides what to do with the parts.
func parseEBPFServerName(name string) (EBPFTarget, bool) {
	parts := strings.SplitN(name, "/", 3)
	if len(parts) != 3 {
		return EBPFTarget{}, false
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return EBPFTarget{}, false
	}
	return EBPFTarget{IP: parts[0], Port: parts[1], FQDN: parts[2]}, true
}
