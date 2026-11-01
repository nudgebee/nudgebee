package core

import (
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

// maxTitleNodeCandidates bounds the IN-list sent to k8s_nodes for one title.
const maxTitleNodeCandidates = 20

// titleHostTokenRegex keeps dots (unlike titleWordSplitter) so a FQDN such as
// "gke-pool-abc-x1y2.us-central1-a.c.proj.internal" survives as one token.
var titleHostTokenRegex = regexp.MustCompile(`[a-z0-9][a-z0-9._-]*`)

// nodeNameCandidatesFromTitle returns the lowercased tokens of title that could be
// a Kubernetes node name: each hostname-shaped token as written, plus its first
// DNS label, because k8s_nodes stores the short name while a monitoring agent
// reports the FQDN. Plain words ("usage", "host") are dropped up front — a node
// name always carries a hyphen, dot or digit — and the caller still requires an
// exact inventory match, so a candidate that is not a node costs one lookup.
func nodeNameCandidatesFromTitle(title string) []string {
	var candidates []string
	seen := map[string]bool{}
	add := func(c string) {
		if len(candidates) >= maxTitleNodeCandidates {
			return
		}
		c = strings.Trim(c, ".-")
		if len(c) < 4 || seen[c] || !strings.ContainsAny(c, "-.0123456789") {
			return
		}
		seen[c] = true
		candidates = append(candidates, c)
	}
	for _, tok := range titleHostTokenRegex.FindAllString(strings.ToLower(title), -1) {
		add(tok)
		if label, _, found := strings.Cut(tok, "."); found {
			add(label)
		}
		if len(candidates) >= maxTitleNodeCandidates {
			break
		}
	}
	return candidates
}

// titleHasCompetingPodShapedToken reports whether title names a pod-shaped
// identity (looksLikeGeneratedPodName) other than matchedName or its DNS label.
// A title like "Pod payments-api-7d9f8c-abcde evicted from node worker-1" is
// really about the pod, even once the pod itself is gone from k8s_pods.
// matchedName is excluded because ordinal node names (worker-1, master-0) match
// the same shape as a real pod name.
func titleHasCompetingPodShapedToken(title, matchedName string) bool {
	matchedName = strings.ToLower(matchedName)
	matchedLabel, _, _ := strings.Cut(matchedName, ".")
	for _, word := range titleWordSplitter.Split(title, -1) {
		if !looksLikeGeneratedPodName(word) {
			continue
		}
		if word == matchedName || word == matchedLabel {
			continue
		}
		return true
	}
	return false
}

// ResolveNodeNameFromTitle scans an alert title for the name of an active
// Kubernetes node in accountIds, returning its inventory name and cloud account,
// or "" on no match, ambiguity, or a competing pod-shaped token (see
// titleHasCompetingPodShapedToken). An exact name match always wins over one
// found only via the node's first DNS label, since that label alone can coincide
// across unrelated nodes (e.g. two regions' "ip-10-0-1-5" hosts). Ambiguity is
// judged on (name, cloud account), not name alone, since the same node name can
// be active in more than one linked account.
func ResolveNodeNameFromTitle(sc *security.RequestContext, title string, accountIds []string) (name, cloudAccountId string) {
	if sc == nil || sc.GetSecurityContext() == nil || len(accountIds) == 0 {
		return "", ""
	}
	candidates := nodeNameCandidatesFromTitle(title)
	if len(candidates) == 0 {
		return "", ""
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("subject_resolution: failed to get database manager", "error", err)
		return "", ""
	}
	var rows []struct {
		Name           string `db:"name"`
		CloudAccountId string `db:"cloud_account_id"`
		ExactMatch     bool   `db:"exact_match"`
	}
	if err := dbms.Db.Select(&rows, `
		SELECT name, cloud_account_id::text AS cloud_account_id, (name = ANY($3)) AS exact_match
		FROM k8s_nodes
		WHERE tenant_id = $1
		  AND cloud_account_id = ANY($2)
		  AND is_active = true
		  AND (name = ANY($3) OR split_part(name, '.', 1) = ANY($3))
		ORDER BY (name = ANY($3)) DESC, updated_at DESC NULLS LAST`,
		sc.GetSecurityContext().GetTenantId(), pq.Array(accountIds), pq.Array(candidates)); err != nil {
		sc.GetLogger().Error("subject_resolution: title node lookup failed", "error", err)
		return "", ""
	}
	hasExact := false
	for _, r := range rows {
		if r.ExactMatch {
			hasExact = true
			break
		}
	}
	for _, r := range rows {
		if hasExact && !r.ExactMatch {
			// An exact match exists elsewhere in the result; a label-only collision
			// against a different, unrelated node does not get a vote.
			continue
		}
		if name == "" {
			name, cloudAccountId = r.Name, r.CloudAccountId
		} else if r.Name != name || r.CloudAccountId != cloudAccountId {
			return "", ""
		}
	}
	if name == "" {
		return "", ""
	}
	if titleHasCompetingPodShapedToken(title, name) {
		return "", ""
	}
	return name, cloudAccountId
}

// ResolveSubjectFromTitleNodeName sets e's subject to the node named in its title,
// tried before the LLM extractor for the same reason as ResolveSubjectFromTitlePodName:
// it is a cheap, exact lookup. Returns true on a match; call only when
// EventSubjectName is empty. A node is not in k8s_workloads, so no workload
// enrichment is attempted.
func ResolveSubjectFromTitleNodeName(sc *security.RequestContext, e *EventIncomingWebhook, accountId string) bool {
	if e == nil || e.EventTitle == "" || accountId == "" {
		return false
	}
	node, _ := ResolveNodeNameFromTitle(sc, e.EventTitle, []string{accountId})
	if node == "" {
		return false
	}
	if e.Investigation.Labels == nil {
		e.Investigation.Labels = map[string]string{}
	}
	e.Investigation.Labels["nb_subject_resolution"] = "title_node_name"
	e.EventSubjectName = node
	e.EventSubjectKind = "node"
	return true
}
