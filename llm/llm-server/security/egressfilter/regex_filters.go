package egressfilter

import "regexp"

// rule is one compiled secret-detection rule. The baseline catalogue here is
// deliberately narrow — only patterns whose match is unambiguous and whose
// presence in an outbound LLM call is almost always a leak.
//
// Callers extend coverage via egressfilter.RegisterRule (see registry.go);
// operators can blank-import a package that does so from its own init().
type rule struct {
	id    string
	regex *regexp.Regexp
}

// baselineRules is the default rule set. Returns a fresh slice on each call so
// callers can mutate without affecting the package default.
//
// Selection criteria for inclusion:
//
//   - The pattern is structurally distinctive enough that false positives are
//     vanishingly rare on prose, code, or config text (e.g. the AKIA/ASIA
//     prefix on AWS access keys, or the PEM "-----BEGIN ... PRIVATE KEY-----"
//     literal header).
//   - Compromise of a single value is high-impact (cloud root credential,
//     long-lived API key, signing key).
//   - The format is stable enough that the rule will not need frequent
//     updates as vendors rotate schemes.
func baselineRules() []rule {
	return []rule{
		// AWS access key id — AKIA/ASIA/ABIA/ACCA prefix + 16 base32-ish chars.
		// Trivially distinctive; no false-positive risk on prose.
		{"aws-access-key-id", regexp.MustCompile(`\b((?:AKIA|ASIA|ABIA|ACCA)[0-9A-Z]{16})\b`)},

		// Private-key PEM headers (RSA, EC, OpenSSH, DSA, encrypted, PGP).
		// Literal string; cannot occur incidentally.
		{"private-key-header", regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |ENCRYPTED |PGP )?PRIVATE KEY-----`)},

		// GitHub personal access tokens — gh[pousr]_ prefix is reserved by
		// GitHub for tokens; will not collide with normal text.
		{"github-pat", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,251}\b`)},

		// OpenAI API key — sk- or sk-proj- prefix, anchored length.
		{"openai-api-key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_\-]{32,}\b`)},

		// Anthropic API key — sk-ant- prefix, anchored length.
		{"anthropic-api-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{32,}\b`)},
	}
}
