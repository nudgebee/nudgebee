package recommendation

import (
	"fmt"
	"regexp"
	"strings"

	"nudgebee/services/account/adapter"

	"k8s.io/apimachinery/pkg/api/resource"
)

// pr_value_verify.go decides whether a refreshed pull request actually carries
// the changed values, by reading the lines it adds (#34959). The agent's own
// report is not used: a run that answered an old review comment reported success
// just the same.

// quantityLine matches a "key: value" line whose value may be a Kubernetes
// quantity — YAML ("cpu: 200m", "- memory: '278Mi'  # note") or JSON
// ("\"cpu\": \"200m\","). Group 1 is the key, group 2 the value.
var quantityLine = regexp.MustCompile(`^\s*-?\s*["']?([^"':#\s]+)["']?\s*:\s*["']?([0-9][0-9.]*[A-Za-z]*)["']?\s*,?\s*(?:#.*)?$`)

// addedQuantity is one added line that could carry a resource value.
type addedQuantity struct {
	dimension string // "cpu", "memory", or "" when the key names neither
	value     resource.Quantity
}

// resourceKeyDimension says which dimension a key can hold. Charts name
// resources whatever their templates read ("cpu", "collectorCpuRequest",
// "app.mem_req"), so the key is matched loosely — but it has to look like a
// resource: "readinessProbeTimeout: 265m" must not vouch for a cpu request of
// 265m. A bare "request"/"limit" key (nested under a cpu: or memory: block the
// diff may not show) can hold either. ok is false for any other key.
func resourceKeyDimension(key string) (dimension string, ok bool) {
	k := strings.ToLower(key)
	switch {
	case strings.Contains(k, "cpu"):
		return "cpu", true
	case strings.Contains(k, "mem"):
		return "memory", true
	case strings.Contains(k, "request"), strings.Contains(k, "limit"):
		return "", true
	}
	return "", false
}

// valuesOnBranch reports whether addedLines — the lines the pull request adds —
// carry every drifted value, and names the first one that is missing.
//
// A value counts as present when an added line with a resource-looking key
// (resourceKeyDimension) for that dimension holds a quantity equal to it,
// written either as the recommendation carries it or as the agent was shown it
// (rendered). Each added line vouches for one value only, so a request and a
// limit that both moved to the same number need two lines. What cannot be told
// apart is which container a line belongs to; a line that is not added
// (because the new value equals the base branch's) reads as missing.
func valuesOnBranch(drifts []valueDrift, rendered map[string]any, addedLines []string) (bool, string) {
	var quantities []addedQuantity
	for _, line := range addedLines {
		m := quantityLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		dimension, ok := resourceKeyDimension(m[1])
		if !ok {
			continue
		}
		if q, err := resource.ParseQuantity(m[2]); err == nil {
			quantities = append(quantities, addedQuantity{dimension: dimension, value: q})
		}
	}
	used := make([]bool, len(quantities))

	for _, d := range drifts {
		shown := renderedField(rendered, d)
		wanted := parseQuantities(d.New, shown)
		found := false
		for i, q := range quantities {
			if used[i] || (q.dimension != "" && q.dimension != d.Dimension) {
				continue
			}
			for _, w := range wanted {
				if q.value.Cmp(w) == 0 {
					used[i], found = true, true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			value := shown
			if value == "" {
				value = d.New
			}
			return false, fmt.Sprintf("%s %s %s %s is not in the pull request's changes", d.Container, d.Dimension, d.Field, value)
		}
	}
	return true, ""
}

// renderedField returns the value the agent was shown for one drifted field.
func renderedField(rendered map[string]any, d valueDrift) string {
	dims, _ := rendered[d.Container].(map[string]any)
	fields, _ := dims[d.Dimension].(map[string]any)
	if v, ok := fields[d.Field]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	return ""
}

func parseQuantities(values ...string) []resource.Quantity {
	var out []resource.Quantity
	for _, v := range values {
		if v == "" {
			continue
		}
		if q, err := resource.ParseQuantity(strings.ReplaceAll(v, ",", "")); err == nil {
			out = append(out, q)
		}
	}
	return out
}

// renderDrifts returns drifts with memory written the way the values file
// writes it, for the text the agent reads. Percentages are unchanged.
func renderDrifts(drifts []valueDrift) []valueDrift {
	out := make([]valueDrift, len(drifts))
	for i, d := range drifts {
		if d.Dimension == "memory" {
			d.New = fmt.Sprintf("%v", adapter.FormatMemoryQuantity(d.New))
			if d.Old != "" {
				d.Old = fmt.Sprintf("%v", adapter.FormatMemoryQuantity(d.Old))
			}
		}
		out[i] = d
	}
	return out
}
