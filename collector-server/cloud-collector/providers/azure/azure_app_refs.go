package azure

import (
	"regexp"
	"strings"
)

// App-config resource-reference extraction.
//
// App Service / Function app settings and connection strings point an app at the
// data resources it uses (storage accounts, key vaults, databases, caches, …).
// We extract ONLY the referenced resource's name/host label — never the secret
// values (keys, passwords, SAS tokens) — so the KG can wire app -> data edges
// without persisting credentials.

// azureRefPattern binds a resource kind to a regex whose first capture group is
// the resource's short name / host label.
type azureRefPattern struct {
	kind string
	re   *regexp.Regexp
}

var azureRefPatterns = []azureRefPattern{
	// storage: AccountName=foo  OR  https://foo.blob|table|queue|file|dfs.core.windows.net
	{"storage", regexp.MustCompile(`(?i)AccountName=([a-z0-9]{3,24})`)},
	{"storage", regexp.MustCompile(`(?i)https?://([a-z0-9]{3,24})\.(?:blob|table|queue|file|dfs)\.core\.windows\.net`)},
	{"keyvault", regexp.MustCompile(`(?i)https?://([a-z0-9][a-z0-9-]{1,22}[a-z0-9])\.vault\.azure\.net`)},
	{"sql", regexp.MustCompile(`(?i)([a-z0-9][a-z0-9-]{0,61}[a-z0-9])\.database\.windows\.net`)},
	{"postgres", regexp.MustCompile(`(?i)([a-z0-9][a-z0-9-]{0,61}[a-z0-9])\.postgres\.database\.azure\.com`)},
	{"mysql", regexp.MustCompile(`(?i)([a-z0-9][a-z0-9-]{0,61}[a-z0-9])\.mysql\.database\.azure\.com`)},
	{"redis", regexp.MustCompile(`(?i)([a-z0-9][a-z0-9-]{0,61}[a-z0-9])\.redis\.cache\.windows\.net`)},
	{"servicebus", regexp.MustCompile(`(?i)([a-z0-9][a-z0-9-]{0,48}[a-z0-9])\.servicebus\.windows\.net`)},
	{"cognitive", regexp.MustCompile(`(?i)https?://([a-z0-9][a-z0-9-]{0,62}[a-z0-9])\.(?:cognitiveservices\.azure\.com|openai\.azure\.com)`)},
}

// azureExtractResourceRefs scans app-config values and returns deduplicated
// {kind, name} references to the resources they mention. Only host/name labels
// are returned — no secret material.
func azureExtractResourceRefs(values []string) []map[string]string {
	seen := make(map[string]bool)
	var refs []map[string]string
	for _, v := range values {
		if v == "" {
			continue
		}
		for _, p := range azureRefPatterns {
			for _, m := range p.re.FindAllStringSubmatch(v, -1) {
				if len(m) < 2 {
					continue
				}
				name := strings.ToLower(m[1])
				key := p.kind + "|" + name
				if seen[key] {
					continue
				}
				seen[key] = true
				refs = append(refs, map[string]string{"kind": p.kind, "name": name})
			}
		}
	}
	return refs
}
