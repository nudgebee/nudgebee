package observability

import (
	"fmt"
	"strings"

	"nudgebee/services/integrations"
	"nudgebee/services/security"
)

// resolveOrionNode finds the Orion NodeID for the VM a request is about.
//
// It mirrors resolveVMInstance, which does the same job against a PromQL store,
// and makes the same judgement on ambiguity: an identifier matching more than one
// node resolves to nothing, because showing another machine's numbers is worse than
// showing none. Orion makes that case real — a node added twice, or a host and its
// management interface, share one IP address.
func resolveOrionNode(ctx *security.RequestContext, req GetUtilisationTrendRequest, meta RequestMetadata) vmLookup {
	probes := orionNodeProbes(meta)
	if len(probes) == 0 {
		return vmLookup{Reason: "this VM carries no IP address or hostname to look up in Orion"}
	}

	cfg, err := integrations.GetOrionConfig(ctx, req.AccountId)
	if err != nil {
		return vmLookup{Reason: "the Orion server could not be queried: " + err.Error()}
	}

	var (
		firstErr  error
		ambiguous []string
	)
	for _, probe := range probes {
		rows, qErr := cfg.Query(ctx.GetContext(), probe.Query, probe.Params)
		if qErr != nil {
			ctx.GetLogger().Warn("observability: Orion node lookup failed",
				"account_id", req.AccountId, "using", probe.Using, "error", qErr)
			if firstErr == nil {
				firstErr = qErr
			}
			continue
		}

		seen := map[string]bool{}
		var ids []string
		for _, row := range rows {
			if id := orionLabel(row["instance"]); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		switch len(ids) {
		case 1:
			if !orionNodeIDValid(ids[0]) {
				continue
			}
			return vmLookup{Instance: ids[0]}
		case 0:
		default:
			ambiguous = ids
		}
	}

	switch {
	case ambiguous != nil:
		return vmLookup{Reason: fmt.Sprintf("more than one Orion node matches this VM (NodeID %s); its identifier is not unique in Orion", strings.Join(ambiguous, ", "))}
	case firstErr != nil:
		return vmLookup{Reason: "the Orion server could not be queried: " + firstErr.Error()}
	}
	return vmLookup{Reason: "no Orion node matching " + vmIdentifiersTried(meta) + " is monitored on this SolarWinds Platform server"}
}
