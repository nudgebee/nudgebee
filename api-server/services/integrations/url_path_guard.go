package integrations

import "strings"

// hasURLPath reports whether raw carries a path, query, or fragment beyond the
// bare host. A single trailing slash is tolerated (https://host/). It handles
// both scheme-prefixed URLs (e.g. SigNoz signoz_url, Elasticsearch url) and
// schemeless hosts (e.g. Datadog site = app.datadoghq.com).
//
// URL-based integration validators use it to reject a pasted browser URL like
// https://champion-cub.in2.signoz.cloud/settings so the connection field holds
// only the base — what validation and every runtime call actually use. (A
// trailing path was previously stripped silently, which let junk like
// .../wty234 save as Enabled and then show up verbatim in the UI.)
func hasURLPath(raw string) bool {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimRight(s, "/")
	return strings.ContainsAny(s, "/?#")
}
