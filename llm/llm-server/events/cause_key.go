package events

import (
	"regexp"
	"strings"
	"unicode"
)

// CauseKey names an alert's cause in three short fields, so two plans of the same alert can be compared
// without a model call (#40734, C8): What failed, How it failed, and Against what it failed (the limit,
// dependency or change it hit). The model fills the fields; NormalizeCauseKey makes them comparable.
type CauseKey struct {
	What    string `json:"what"`
	How     string `json:"how"`
	Against string `json:"against"`
}

// causeKeySeparators are the runs of characters that separate words in a cause field.
var causeKeySeparators = regexp.MustCompile(`[\s_\-./:,;|]+`)

// causeKeyWords splits words written joined, so every spelling of a reason reaches the same phrase below
// (ImagePullBackoff, image pull backoff and image pull back off all read "image pull back off"). Whole words.
var causeKeyWords = []struct{ from, to string }{
	{"backoff", "back off"},
	{"imagepull", "image pull"},
}

// causeKeySynonyms maps a normalized field (or a phrase inside one) to its canonical form. Applied to whole
// words only, longest phrase first.
var causeKeySynonyms = []struct{ from, to string }{
	{"out of memory killed", "oom"},
	{"out of memory", "oom"},
	{"oom killed", "oom"},
	{"oomkilled", "oom"},
	{"oomkill", "oom"},
	{"crash loop back off", "crashloop"},
	{"crashloop back off", "crashloop"},
	{"crashloopbackoff", "crashloop"},
	{"crash loop", "crashloop"},
	{"crashlooping", "crashloop"},
	{"image pull back off", "image pull"},
	{"imagepullbackoff", "image pull"},
	{"errimagepull", "image pull"},
	{"err image pull", "image pull"},
	{"createcontainerconfigerror", "create container config error"},
	{"mem limit", "memory limit"},
	{"cpu throttling", "cpu throttle"},
	{"cpu throttled", "cpu throttle"},
	{"throttling", "throttle"},
	{"throttled", "throttle"},
	{"time out", "timeout"},
	{"timed out", "timeout"},
	{"timeouts", "timeout"},
	{"deployment", "deploy"},
	{"deployments", "deploy"},
	{"statefulset", "sts"},
	{"statefulsets", "sts"},
	{"daemonset", "ds"},
	{"daemonsets", "ds"},
	{"configmap", "cm"},
	{"configmaps", "cm"},
	{"persistentvolumeclaim", "pvc"},
	{"persistent volume claim", "pvc"},
}

// NormalizeCauseKey makes each field comparable: CamelCase split into words (Kubernetes reasons such as
// ImagePullBackOff), lower-case, trimmed, every run of separators collapsed to one space, and known synonyms mapped
// to one form (OOMKilled -> oom).
func NormalizeCauseKey(k CauseKey) CauseKey {
	return CauseKey{What: normalizeCauseField(k.What), How: normalizeCauseField(k.How), Against: normalizeCauseField(k.Against)}
}

func normalizeCauseField(s string) string {
	s = strings.TrimSpace(causeKeySeparators.ReplaceAllString(strings.ToLower(splitCamelCase(s)), " "))
	if s == "" {
		return ""
	}
	padded := " " + s + " "
	for _, list := range [][]struct{ from, to string }{causeKeyWords, causeKeySynonyms} {
		for _, syn := range list {
			padded = strings.ReplaceAll(padded, " "+syn.from+" ", " "+syn.to+" ")
		}
	}
	return strings.TrimSpace(padded)
}

// splitCamelCase puts a space where a CamelCase word starts: after a lower-case letter or digit, and before the
// last capital of a run followed by a lower-case letter (OOMKilled -> OOM Killed, ImagePullBackOff -> Image Pull
// Back Off). Text without capitals inside a word is unchanged.
func splitCamelCase(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Comparable reports whether the key can be compared: every field is set.
func (k CauseKey) Comparable() bool {
	n := NormalizeCauseKey(k)
	return n.What != "" && n.How != "" && n.Against != ""
}

// SameCause reports whether a and b name the same cause: both comparable and equal once normalized. A key with
// an empty field is never the same as another.
func SameCause(a, b CauseKey) bool {
	return a.Comparable() && b.Comparable() && NormalizeCauseKey(a) == NormalizeCauseKey(b)
}
