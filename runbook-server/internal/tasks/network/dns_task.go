package network

import (
	"fmt"
	"net"
	"nudgebee/runbook/internal/tasks/types"
	"strings"
)

// DnsTask implements the Task interface for performing DNS lookups.
type DnsTask struct{}

func (t *DnsTask) GetName() string {
	return "network.dns"
}

func (t *DnsTask) GetDescription() string {
	return "Look up DNS records for a domain."
}

func (t *DnsTask) GetDisplayName() string {
	return "DNS Lookup"
}

func (t *DnsTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	domain, ok := params["domain"].(string)
	if !ok || domain == "" {
		return nil, fmt.Errorf("domain parameter is required and must be a non-empty string")
	}

	recordType := "A"
	if val, ok := params["type"].(string); ok && val != "" {
		recordType = strings.ToUpper(val)
	}

	// Future: Support custom DNS server using net.Resolver with specific Dial function.
	// For now, use system resolver.

	var results any
	var err error

	switch recordType {
	case "A":
		ips, lookupErr := net.DefaultResolver.LookupIP(taskCtx.GetContext(), "ip4", domain)
		if lookupErr != nil {
			err = lookupErr
		} else {
			var filtered []string
			for _, ip := range ips {
				filtered = append(filtered, ip.String())
			}
			results = filtered
		}
	case "AAAA":
		ips, lookupErr := net.DefaultResolver.LookupIP(taskCtx.GetContext(), "ip6", domain)
		if lookupErr != nil {
			err = lookupErr
		} else {
			var filtered []string
			for _, ip := range ips {
				filtered = append(filtered, ip.String())
			}
			results = filtered
		}
	case "CNAME":
		cname, lookupErr := net.DefaultResolver.LookupCNAME(taskCtx.GetContext(), domain)
		if lookupErr != nil {
			err = lookupErr
		} else {
			results = cname
		}
	case "TXT":
		txts, lookupErr := net.DefaultResolver.LookupTXT(taskCtx.GetContext(), domain)
		if lookupErr != nil {
			err = lookupErr
		} else {
			results = txts
		}
	case "MX":
		mxs, lookupErr := net.DefaultResolver.LookupMX(taskCtx.GetContext(), domain)
		if lookupErr != nil {
			err = lookupErr
		} else {
			var mxRecords []map[string]any
			for _, mx := range mxs {
				mxRecords = append(mxRecords, map[string]any{
					"host": mx.Host,
					"pref": mx.Pref,
				})
			}
			results = mxRecords
		}
	case "NS":
		nss, lookupErr := net.DefaultResolver.LookupNS(taskCtx.GetContext(), domain)
		if lookupErr != nil {
			err = lookupErr
		} else {
			var nsRecords []string
			for _, ns := range nss {
				nsRecords = append(nsRecords, ns.Host)
			}
			results = nsRecords
		}
	default:
		return nil, fmt.Errorf("unsupported record type: %s", recordType)
	}

	if err != nil {
		// Wrap error to be more friendly, or return as is?
		// Task framework handles errors.
		return nil, fmt.Errorf("dns lookup failed: %w", err)
	}

	return map[string]any{
		"domain": domain,
		"type":   recordType,
		"answer": results,
	}, nil
}

func (t *DnsTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"domain": {
				Type:        "string",
				Description: "The domain name to query.",
				Required:    true,
			},
			"type": {
				Type:        "string",
				Description: "The DNS record type (A, AAAA, CNAME, MX, TXT, NS). Defaults to 'A'.",
				Required:    false,
			},
		},
	}
}

func (t *DnsTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"domain": {
				Type:        "string",
				Description: "The queried domain.",
				Required:    true,
			},
			"type": {
				Type:        "string",
				Description: "The queried record type.",
				Required:    true,
			},
			"answer": {
				Type:        "any", // Can be string, []string, or []map
				Description: "The DNS query results.",
				Required:    true,
			},
		},
	}
}
