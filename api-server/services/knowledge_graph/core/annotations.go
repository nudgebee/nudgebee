package core

// Kubernetes annotations are free-form, and some tools write a whole copy of
// the object into one. kubectl apply stores the applied manifest in
// kubectl.kubernetes.io/last-applied-configuration, which for a Secret is the
// full base64 data and for a workload is every inline env value. Storing the
// annotations map verbatim would put those values in knowledge_graph_node,
// where kg_get_node returns node properties to the AI.
//
// SanitizeAnnotations is applied by NewNode, so every source that puts an
// "annotations" map on a node goes through it.

// annotationsCarryingObject are annotations whose value is a serialized copy of
// the whole object. They are dropped on every node kind.
var annotationsCarryingObject = []string{
	"kubectl.kubernetes.io/last-applied-configuration",
	// Rancher / k3s wrangler apply: gzipped, base64 copy of the applied object.
	"objectset.rio.cattle.io/applied",
}

// secretAnnotationValueAllowlist are the Secret annotations whose values are
// names, not material, and are worth keeping. Every other Secret annotation
// keeps its key and loses its value.
var secretAnnotationValueAllowlist = map[string]bool{
	"meta.helm.sh/release-name":          true,
	"meta.helm.sh/release-namespace":     true,
	"kubernetes.io/service-account.name": true,
	"kubernetes.io/service-account.uid":  true,
	"cert-manager.io/certificate-name":   true,
	"cert-manager.io/issuer-name":        true,
	"cert-manager.io/issuer-kind":        true,
	"cert-manager.io/issuer-group":       true,
}

// RedactedAnnotationValue replaces a Secret annotation value that is not on
// the allowlist.
const RedactedAnnotationValue = "[redacted]"

// SanitizeAnnotations returns a copy of an annotations map (map[string]any or
// map[string]string) with object-copy annotations removed and, for Secrets,
// every value outside the allowlist redacted. It returns nil when nothing is
// left or the input is not an annotations map; the input is never modified.
func SanitizeAnnotations(nodeType NodeType, raw any) map[string]any {
	out := map[string]any{}
	switch m := raw.(type) {
	case map[string]any:
		for k, v := range m {
			out[k] = v
		}
	case map[string]string:
		for k, v := range m {
			out[k] = v
		}
	default:
		return nil
	}
	for _, k := range annotationsCarryingObject {
		delete(out, k)
	}
	if nodeType == NodeTypeK8sSecret {
		for k := range out {
			if !secretAnnotationValueAllowlist[k] {
				out[k] = RedactedAnnotationValue
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeNodeAnnotations applies SanitizeAnnotations to properties in place.
func sanitizeNodeAnnotations(nodeType NodeType, properties map[string]any) {
	raw, ok := properties["annotations"]
	if !ok {
		return
	}
	if clean := SanitizeAnnotations(nodeType, raw); clean != nil {
		properties["annotations"] = clean
	} else {
		delete(properties, "annotations")
	}
}
