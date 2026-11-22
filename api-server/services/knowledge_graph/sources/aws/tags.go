package aws

import "regexp"

// awsUniqueIDLen is the length of every AWS unique identifier: a 4-char prefix
// plus 16 base32-ish chars. Used as a cheap pre-check so the regex below is not
// evaluated for an ordinary tag key — this runs once per tag of every inventory
// row, and one account's ingest can carry 500k+ rows in a single build.
const awsUniqueIDLen = 20

// awsUniqueIDPattern matches a tag key that is an AWS unique identifier rather
// than a name someone chose. The AWS console stores an access key's description
// as a tag on the key's IAM user keyed by the access key ID, so access key IDs
// reach the graph as ordinary user tags; publishing them as labels puts a
// credential identifier in the Label filter's suggestions and in the Labels
// table of every IAM user node.
//
// cloud-collector drops these at collection, so this is a second line of
// defence covering rows already stored. The prefix set mirrors the redaction
// scrubbers in services/audit/redact.go.
var awsUniqueIDPattern = regexp.MustCompile(`^(?:AKIA|ASIA|AGPA|AIDA|AROA|ANPA|ANVA)[0-9A-Z]{16}$`)

// isAWSUniqueIDTagKey reports whether a tag key is an AWS unique identifier
// (access key ID, user ID, role ID, …), which must not become a node label.
func isAWSUniqueIDTagKey(key string) bool {
	return len(key) == awsUniqueIDLen && awsUniqueIDPattern.MatchString(key)
}

// dropAWSUniqueIDTagKeys removes every AWS-unique-identifier key from an
// unmarshalled tags map, in place. Deleting while ranging over a map is
// specified to be safe in Go.
func dropAWSUniqueIDTagKeys(tags map[string]interface{}) {
	for key := range tags {
		if isAWSUniqueIDTagKey(key) {
			delete(tags, key)
		}
	}
}
