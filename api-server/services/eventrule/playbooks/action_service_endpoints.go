package playbooks

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// service_endpoints_enricher adds LIVE state to service_no_endpoints
// findings. The agent-side evidence is a snapshot at fire time; by the time
// someone investigates, the selector may have been fixed or the workload
// recreated. This enricher re-resolves, via relay:
//
//  1. the Service's current spec.selector,
//  2. its current Endpoints (address count — the authoritative signal),
//  3. every Deployment/StatefulSet/DaemonSet pod template in the namespace,
//     so the selector-vs-template mismatch (and the closest candidate) is
//     visible on the card.
//
// Output is a markdown block routed to the existing TextEnricherDynamicCard
// via additional_info.actual_action_name="text_enricher" — deliberately no
// new frontend actionType/table-name registration needed.
type serviceEndpointsAction struct{}

func (a *serviceEndpointsAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	ev := ctx.GetEvent()
	if ev.AggregationKey != "service_no_endpoints" {
		return false
	}
	name, ns := subjectServiceNamespace(ev)
	return name != "" && ns != ""
}

func (a *serviceEndpointsAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	name, ns := subjectServiceNamespace(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"service_name": name, "namespace": ns})
}

func (a *serviceEndpointsAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	serviceName, _ := rawParams["service_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if serviceName == "" || namespace == "" {
		return nil, errors.New("service_endpoints_enricher: service_name + namespace required")
	}

	svcData, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "services",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
		"name":           []string{serviceName},
	})
	if err != nil {
		return nil, fmt.Errorf("service_endpoints_enricher: get service: %w", err)
	}
	svc := firstResourceDict(svcData)
	if svc == nil {
		return nil, errors.New("service_endpoints_enricher: service not found (deleted since the finding fired?)")
	}
	selector := serviceSelectorFromDict(svc)

	// Endpoints object shares the Service's name. A get error is non-fatal —
	// the selector/template comparison is still worth showing.
	addressCount := -1 // -1 = unknown
	epData, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "endpoints",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
		"name":           []string{serviceName},
	})
	if err == nil {
		if ep := firstResourceDict(epData); ep != nil {
			addressCount = endpointsAddressCount(ep)
		}
	}

	// One relay call for all three workload kinds — GetResource concatenates
	// comma-separated resource types into a flat array.
	var workloads []workloadTemplate
	wlData, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "deployments,statefulsets,daemonsets",
		"group":          "apps",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
	})
	if err == nil {
		workloads = workloadTemplatesFromList(wlData)
	}

	text, insights := buildServiceEndpointsReport(namespace, serviceName, selector, addressCount, workloads)

	return serviceEndpointsMarkdownResponse{
		Title: "Service endpoint status (live)",
		Data:  text,
		AdditionalInfo: map[string]any{
			"title":       "Service endpoint status (live)",
			"action_name": "service_endpoints_enricher",
			// Route to the generic TextEnricherDynamicCard — no frontend
			// registration needed for a new action type.
			"actual_action_name": "text_enricher",
			"service_name":       serviceName,
			"namespace":          namespace,
		},
		Insight: insights,
	}, nil
}

// buildServiceEndpointsReport renders the markdown body + insights. Split out
// (pure) so tests can cover the broken / recovered / selector-removed states
// without a relay.
func buildServiceEndpointsReport(namespace, serviceName string, selector map[string]string, addressCount int, workloads []workloadTemplate) (string, []PlaybookActionResponseInsight) {
	var b strings.Builder
	insights := []PlaybookActionResponseInsight{}
	selStr := formatLabelMap(selector)

	fmt.Fprintf(&b, "**Live check for Service `%s/%s`**\n\n", namespace, serviceName)
	switch {
	case len(selector) == 0:
		b.WriteString("- Selector: *(none — selector was removed since the finding fired)*\n")
	default:
		fmt.Fprintf(&b, "- Selector: `%s`\n", selStr)
	}
	switch {
	case addressCount < 0:
		b.WriteString("- Endpoints: *unavailable (could not fetch)*\n")
	case addressCount == 0:
		b.WriteString("- Endpoints: **0 addresses — still not routing traffic**\n")
	default:
		fmt.Fprintf(&b, "- Endpoints: **%d address(es) — appears recovered**\n", addressCount)
	}

	anyMatch := false
	if len(workloads) > 0 && len(selector) > 0 {
		fmt.Fprintf(&b, "\n**Workload pod templates in `%s`:**\n\n", namespace)
		b.WriteString("| Workload | Template labels | Matches selector |\n|---|---|---|\n")
		for _, w := range workloads {
			match := labelsContain(w.labels, selector)
			anyMatch = anyMatch || match
			mark := "✗"
			if match {
				mark = "✓"
			}
			fmt.Fprintf(&b, "| %s | `%s` | %s |\n", w.name, formatLabelMap(w.labels), mark)
		}
	}

	switch {
	case addressCount > 0:
		insights = append(insights, PlaybookActionResponseInsight{
			Severity: "Info",
			Message: fmt.Sprintf("Service %s/%s now has %d endpoint address(es) — the selector mismatch appears resolved",
				namespace, serviceName, addressCount),
		})
	case len(selector) > 0 && !anyMatch:
		insights = append(insights, PlaybookActionResponseInsight{
			Severity: "Critical",
			Message: fmt.Sprintf("Service %s/%s still has no endpoints: selector (%s) matches none of the %d workload pod template(s) in the namespace",
				namespace, serviceName, selStr, len(workloads)),
		})
	case len(selector) > 0 && anyMatch && addressCount == 0:
		insights = append(insights, PlaybookActionResponseInsight{
			Severity: "High",
			Message: fmt.Sprintf("Service %s/%s selector now matches a workload template but there are still 0 endpoint addresses — the workload may be scaled to zero or its pods not ready",
				namespace, serviceName),
		})
	}
	return b.String(), insights
}

// serviceEndpointsMarkdownResponse is a markdown evidence payload with a
// top-level `data` string — the shape TextEnricherDynamicCard renders
// (`data.data` markdown body, `data.title` card title). The package's
// PlaybookActionResponseMarkdown serialises its body under `text`, which
// that card does not read; this response exists to hit the text_enricher
// render path without frontend changes.
type serviceEndpointsMarkdownResponse struct {
	Title          string                          `json:"title"`
	Data           string                          `json:"data"`
	AdditionalInfo map[string]any                  `json:"additional_info"`
	Insight        []PlaybookActionResponseInsight `json:"insight"`
}

func (m serviceEndpointsMarkdownResponse) GetFormatName() string { return "markdown" }
func (m serviceEndpointsMarkdownResponse) GetData() any          { return m.Data }
func (m serviceEndpointsMarkdownResponse) GetAdditionalInfo() map[string]any {
	return m.AdditionalInfo
}
func (m serviceEndpointsMarkdownResponse) GetInsights() []PlaybookActionResponseInsight {
	return m.Insight
}

// subjectServiceNamespace extracts (name, namespace) for a service-subject
// event. Returns empty strings when the subject is not a Service.
func subjectServiceNamespace(event PlaybookEvent) (string, string) {
	if !strings.EqualFold(event.SubjectType, "service") {
		return "", ""
	}
	return event.SubjectName, event.SubjectNamespace
}

// workloadTemplate is one workload's name + pod-template labels.
type workloadTemplate struct {
	name   string
	labels map[string]string
}

// serviceSelectorFromDict reads spec.selector off a (snake-cased) Service
// payload. Selector keys are user label names; SnakeKeysDeep leaves keys
// containing "." / "/" / "-" untouched, so real-world selectors survive.
func serviceSelectorFromDict(svc map[string]any) map[string]string {
	spec, _ := svc["spec"].(map[string]any)
	if spec == nil {
		return nil
	}
	raw, _ := spec["selector"].(map[string]any)
	return stringMap(raw)
}

// endpointsAddressCount counts subsets[].addresses[] on an Endpoints payload
// (ready addresses only — notReadyAddresses don't receive traffic).
func endpointsAddressCount(ep map[string]any) int {
	count := 0
	subsets, _ := ep["subsets"].([]any)
	for _, s := range subsets {
		sm, _ := s.(map[string]any)
		if sm == nil {
			continue
		}
		addrs := getArrayField(sm, "addresses")
		count += len(addrs)
	}
	return count
}

// workloadTemplatesFromList extracts (name, spec.template.metadata.labels)
// from a flat get_resource list of workloads.
func workloadTemplatesFromList(data any) []workloadTemplate {
	items, _ := data.([]any)
	out := make([]workloadTemplate, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		meta, _ := m["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if name == "" {
			continue
		}
		spec := getMapField(m, "spec")
		tmpl := getMapField(spec, "template")
		tmplMeta := getMapField(tmpl, "metadata")
		labels, _ := tmplMeta["labels"].(map[string]any)
		out = append(out, workloadTemplate{name: name, labels: stringMap(labels)})
	}
	return out
}

// labelsContain reports whether labels carries every pair in selector
// (K8s Service selectors are plain equality maps).
func labelsContain(labels, selector map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		// Comma-ok: a missing key must not match a selector pair with an
		// empty-string value (labels[k] would zero-value to "" and compare
		// equal).
		val, ok := labels[k]
		if !ok || val != v {
			return false
		}
	}
	return true
}

// formatLabelMap renders a label map as sorted "k=v,k=v" for stable display.
func formatLabelMap(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func stringMap(raw map[string]any) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
