package account

import "strings"

// maxVerbTokens caps how much of a command is echoed into logs. Four covers the
// longest shape we issue (`gcloud compute backend-services list`).
const maxVerbTokens = 4

// maxVerbTokenLen rejects implausibly long tokens; every real CLI verb is short.
const maxVerbTokenLen = 40

// CommandVerb reduces a cloud CLI command to its leading subcommand path, e.g.
//
//	aws ec2 describe-network-interfaces --filters "Name=addresses..."  -> aws.ec2.describe-network-interfaces
//	gcloud compute backend-services list --format=json                 -> gcloud.compute.backend-services.list
//	az login --service-principal -u <id> -p <secret>                   -> az.login
//
// Only the verb is safe to log. The full command string carries ARNs, resource
// ids and — on the Azure login path — a client secret, so this stops at the
// first token that is not a bare lowercase subcommand word: any flag, any value
// with a scheme/path/quote in it, anything over maxVerbTokenLen. Every command
// we issue passes its arguments behind flags, so the scan stops before the first
// argument in practice.
//
// Returns "unknown" for an empty or entirely unparseable command so the log
// field is always present and always groupable.
func CommandVerb(command string) string {
	tokens := strings.Fields(command)
	verb := make([]string, 0, maxVerbTokens)

	for _, token := range tokens {
		if len(verb) == maxVerbTokens || !isVerbToken(token) {
			break
		}
		verb = append(verb, token)
	}

	if len(verb) == 0 {
		return "unknown"
	}
	return strings.Join(verb, ".")
}

// isVerbToken reports whether a token is a bare subcommand word: lowercase
// letter first, then lowercase alphanumerics and dashes only. Digits are allowed
// mid-token because real verbs contain them (`ec2`, `elbv2`, `route53`).
func isVerbToken(token string) bool {
	if token == "" || len(token) > maxVerbTokenLen {
		return false
	}
	if token[0] < 'a' || token[0] > 'z' {
		return false
	}
	for i := 1; i < len(token); i++ {
		c := token[i]
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		if !isLower && !isDigit && c != '-' {
			return false
		}
	}
	return true
}
