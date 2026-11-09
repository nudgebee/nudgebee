package egressfilter

import (
	"regexp"
	"strings"
)

// commandLinePlaceholder replaces a secret RedactCommandLine finds.
const commandLinePlaceholder = "[REDACTED:command-line]"

// Each argument of a command relayed from the workspace arrives quoted
// (kubectl 'exec' 'db-0' '--' 'mysql' '-pHunter2'), so the flag rules below
// accept a quote before a flag and around its value.
var (
	// --password hunter2, --token=abc, -api-key 'x': a flag whose name says it
	// carries a secret, and its value (a value that is another flag is left).
	commandLineSecretFlag = regexp.MustCompile(`(?i)(\s|^)(['"]?)(--?[a-z0-9_.-]*(?:password|passwd|pwd|token|secret|api[-_]?key|access[-_]?key|private[-_]?key|credentials?)[a-z0-9_.-]*)(['"]?)(=|\s+)('[^'-][^']*'|"[^"-][^"]*"|[^\s'"=-][^\s'"]*)`)
	// mysql/psql-family -p: "-p hunter2" or "-phunter2".
	commandLineDBClient       = regexp.MustCompile(`(?i)(^|[\s/;|&("'])(mysql|mysqldump|mysqladmin|mariadb|mariadb-dump|psql|pg_dump|pg_dumpall|pg_restore)(\s|$|['"])`)
	commandLineDBPasswordFlag = regexp.MustCompile(`(\s['"]?-p)(?:(['"]?\s+)('[^'-][^']*'|"[^"-][^"]*"|[^\s'"-][^\s'"]*)|([^\s'"]+))`)
	// redis-cli -a / --pass.
	commandLineRedisPassword = regexp.MustCompile(`(?i)(redis-cli\b[^;|&]*?\s['"]?(?:-a|--pass)['"]?)(\s+)('[^']*'|"[^"]*"|\S+)`)
	// curl -u user:pass / --user user:pass: the password after the colon.
	commandLineCurlUser = regexp.MustCompile(`(?i)(\bcurl\b[^;|&]*?\s['"]?(?:-u|--user)['"]?(?:=|\s+)['"]?[^\s:'"]+:)([^\s'"]+)`)
	// https://user:pass@host: the password part of URL userinfo.
	commandLineURLUserinfo = regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://[^/\s:@'"]+:)([^@\s/'"]+)(@)`)
	// Authorization: Bearer abc, X-Api-Key: abc, as header values.
	commandLineAuthHeader = regexp.MustCompile(`(?i)((?:proxy-)?authorization:\s*(?:bearer|basic|token|digest)?\s*)([^\s'"]+)`)
	commandLineKeyHeader  = regexp.MustCompile(`(?i)((?:x-)?(?:api|auth|access)[-_]?(?:key|token):\s*)([^\s'"]+)`)
	// PGPASSWORD=x psql, AWS_SECRET_ACCESS_KEY=... aws: an environment
	// assignment whose name says it carries a secret.
	commandLineSecretEnv = regexp.MustCompile(`(?i)(^|[\s;|&("'])((?:export\s+)?[a-z_][a-z0-9_]*(?:password|passwd|pwd|token|secret|key|credentials?)[a-z0-9_]*=)('[^']*'|"[^"]*"|[^\s'";|&]+)`)
)

// RedactCommandLine masks the secrets a command line carries in its shape —
// values of password/token/secret/key-like flags, a mysql/psql -p password,
// a redis-cli -a password, a curl -u password, URL userinfo passwords,
// Authorization and API-key header values, and environment assignments with
// secret-like names — which
// RedactSecrets (built for tool output) does not all recognise. It is meant to
// run before RedactSecrets wherever a command is shown or stored outside the
// run, such as a bee run's recorded commands. The chat approval cards show
// commands unredacted today and could reuse it.
//
// It over-masks rather than under-masks: a value that only looks secret, such
// as a psql -p port, is masked too.
func RedactCommandLine(command string) string {
	if strings.TrimSpace(command) == "" {
		return command
	}
	out := commandLineSecretEnv.ReplaceAllString(command, "${1}${2}"+commandLinePlaceholder)
	out = commandLineSecretFlag.ReplaceAllString(out, "${1}${2}${3}${4}${5}"+commandLinePlaceholder)
	if commandLineDBClient.MatchString(out) {
		out = commandLineDBPasswordFlag.ReplaceAllStringFunc(out, func(match string) string {
			// sub[1] is the flag; "-p hunter2" fills sub[2] (the separator)
			// and sub[3], "-phunter2" fills sub[4].
			sub := commandLineDBPasswordFlag.FindStringSubmatch(match)
			if strings.HasPrefix(sub[3]+sub[4], "[REDACTED") {
				return match
			}
			return sub[1] + sub[2] + commandLinePlaceholder
		})
	}
	out = commandLineRedisPassword.ReplaceAllString(out, "${1}${2}"+commandLinePlaceholder)
	out = commandLineCurlUser.ReplaceAllString(out, "${1}"+commandLinePlaceholder)
	out = commandLineURLUserinfo.ReplaceAllString(out, "${1}"+commandLinePlaceholder+"${3}")
	out = commandLineAuthHeader.ReplaceAllString(out, "${1}"+commandLinePlaceholder)
	out = commandLineKeyHeader.ReplaceAllString(out, "${1}"+commandLinePlaceholder)
	return out
}
