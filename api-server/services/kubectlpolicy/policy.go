// Package kubectlpolicy refuses kubectl commands that would read or write a
// credential-bearing resource, or a credential file on the agent pod.
//
// It is applied where a kubectl command string from a user, an automation or a
// model enters the api-server on its way to the in-cluster agent
// (kubectl_command_executor). The agent enforces the same rules itself
// (k8s-agent runner/pkg/kube/secretpolicy.go), but customers upgrade agents
// slowly, so the backend refuses first. The rules mirror llm-server
// tools.ValidateKubectlRelayCommand; runbook-server carries a copy in
// services/relay/kubectl_policy.go. Keep the three in step.
//
// It is a denylist, not a parser. When in doubt it refuses: a false refusal is
// a clear error.
package kubectlpolicy

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// secretBearingKinds are resource kinds whose objects hold credentials, in
// every spelling kubectl accepts: singular, plural and the short names the
// External Secrets Operator and Secrets Store CSI CRDs declare. `ss` is
// SecretStore; statefulsets are `sts`.
var secretBearingKinds = map[string]bool{
	"secret": true, "secrets": true,
	"sealedsecret": true, "sealedsecrets": true,
	"externalsecret": true, "externalsecrets": true, "es": true,
	"secretproviderclass": true, "secretproviderclasses": true, "spc": true, "spcs": true,
	"clustersecretstore": true, "clustersecretstores": true, "css": true,
	"secretstore": true, "secretstores": true, "ss": true,
}

// secretBearingAPIResources are the same kinds as an API path names them
// (`--raw /api/v1/namespaces/x/secrets`). The API server routes only singular
// and plural names, so the short names are left out here.
var secretBearingAPIResources = map[string]bool{
	"secret": true, "secrets": true,
	"sealedsecret": true, "sealedsecrets": true,
	"externalsecret": true, "externalsecrets": true,
	"secretproviderclass": true, "secretproviderclasses": true,
	"clustersecretstore": true, "clustersecretstores": true,
	"secretstore": true, "secretstores": true,
}

// resourceTakingVerbs are the subcommands whose positional arguments name
// resource kinds. Write verbs are included: Secrets are not created, edited or
// deleted through this path either.
var resourceTakingVerbs = map[string]bool{
	"get": true, "describe": true, "top": true, "explain": true, "wait": true,
	"create": true, "delete": true, "edit": true, "apply": true, "replace": true,
	"scale": true, "rollout": true, "set": true,
	"label": true, "annotate": true, "patch": true,
}

// mountedSecretPaths are where kubelet mounts ServiceAccount tokens and Secret
// volumes. No leading slash, so relative spellings match too.
var mountedSecretPaths = []string{
	"var/run/secrets",
	"run/secrets",
	"var/lib/kubelet/pods",
	"kubernetes.io/serviceaccount",
}

// quoteStripper removes what a shell or shlex reads as nothing, so `sec""ret`,
// `'secret'` and `s\ecret` are seen as `secret`.
var quoteStripper = strings.NewReplacer(`"`, "", "'", "", `\`, "")

// metacharSplitter turns shell separators into spaces so `secret;` or
// `$(echo secret)` yield a bare `secret` token.
var metacharSplitter = strings.NewReplacer(
	";", " ", "&", " ", "|", " ", ">", " ", "<", " ",
	"(", " ", ")", " ", "`", " ", "$", " ", "\n", " ", "\r", " ",
)

// CheckSecretAccess returns an error when command would reach a Secret or a
// Secret-like resource, a mounted credential file, or mint a token.
func CheckSecretAccess(command string) error {
	tokens := tokenize(command)
	if err := checkRawPaths(tokens); err != nil {
		return err
	}
	if err := checkOutputFormats(tokens); err != nil {
		return err
	}
	if p := mountedSecretPathIn(tokens); p != "" {
		return fmt.Errorf("kubectl: reading mounted secret paths (%s) is blocked", p)
	}
	verbAt := -1
	for i, t := range tokens {
		if !strings.HasPrefix(t, "-") && resourceTakingVerbs[strings.ToLower(t)] {
			verbAt = i
			break
		}
	}
	if verbAt < 0 {
		return nil
	}
	if strings.ToLower(tokens[verbAt]) == "create" && contains(tokens[verbAt+1:], "token") {
		return errors.New("kubectl: minting ServiceAccount tokens (create token) is blocked")
	}
	if kind := secretBearingKindIn(tokens[verbAt+1:]); kind != "" {
		return fmt.Errorf("kubectl: access to %q is blocked. Secret-bearing kinds (secrets, sealedsecrets, externalsecrets, secretstores, secretproviderclasses) are not readable through Nudgebee", kind)
	}
	return nil
}

func tokenize(command string) []string {
	cmd := quoteStripper.Replace(command)
	cmd = metacharSplitter.Replace(cmd)
	tokens := strings.Fields(cmd)
	if len(tokens) > 0 && (tokens[0] == "kubectl" || strings.HasSuffix(tokens[0], "/kubectl")) {
		tokens = tokens[1:]
	}
	return tokens
}

// secretBearingKindIn returns the first secret-bearing kind named by a token
// after the verb, or "". Positional tokens and the value of `--flag=value`
// (`--from=secret/x`) are checked. Each is split on `,` first (`pods,secrets`),
// then stripped of `/name` and `.group` (`secret/x`, `secrets.v1.`).
//
// Values of space-separated flags are checked as positionals, since which flags
// take a value is not tracked: a namespace named `secrets` or `es` is refused.
func secretBearingKindIn(tokens []string) string {
	for _, t := range tokens {
		if strings.HasPrefix(t, "-") {
			_, v, ok := strings.Cut(t, "=")
			if !ok {
				continue
			}
			t = v
		}
		for part := range strings.SplitSeq(t, ",") {
			part, _, _ = strings.Cut(part, "/")
			part, _, _ = strings.Cut(part, ".")
			part = strings.ToLower(part)
			if secretBearingKinds[part] {
				return part
			}
		}
	}
	return ""
}

// checkRawPaths refuses `--raw PATH` when PATH addresses a secret-bearing
// resource or ServiceAccount token minting. The path is percent-decoded until
// stable, lowercased and cleaned, since the API server decodes it before
// routing (`%73ecrets` reaches the secrets handler).
func checkRawPaths(tokens []string) error {
	for i, t := range tokens {
		var raw string
		switch {
		case t == "--raw" && i+1 < len(tokens):
			raw = tokens[i+1]
		case strings.HasPrefix(t, "--raw="):
			raw = strings.TrimPrefix(t, "--raw=")
		default:
			continue
		}
		p := raw
		for range 3 {
			decoded, err := url.PathUnescape(p)
			if err != nil {
				return fmt.Errorf("kubectl: --raw path %q is not a valid URL path", raw)
			}
			if decoded == p {
				break
			}
			p = decoded
		}
		p, _, _ = strings.Cut(p, "?")
		p = strings.ToLower(p)
		for _, form := range []string{p, path.Clean("/" + p)} {
			segs := strings.Split(form, "/")
			for j, s := range segs {
				if secretBearingAPIResources[s] {
					return fmt.Errorf("kubectl: --raw access to %q is blocked. Secret-bearing resources are not readable through Nudgebee", s)
				}
				if s == "token" && j >= 2 && segs[j-2] == "serviceaccounts" {
					return errors.New("kubectl: --raw access to ServiceAccount tokens is blocked")
				}
			}
		}
	}
	return nil
}

// checkOutputFormats refuses file-backed output formats (`-o jsonpath-file=`,
// `go-template-file`, `custom-columns-file`, `templatefile`). kubectl reads the
// file on the agent pod as a template and echoes it on a parse error, the
// agent's own ServiceAccount token included.
func checkOutputFormats(tokens []string) error {
	for i, t := range tokens {
		var v string
		switch {
		case (t == "-o" || t == "--output") && i+1 < len(tokens):
			v = tokens[i+1]
		case strings.HasPrefix(t, "--output="):
			v = strings.TrimPrefix(t, "--output=")
		case strings.HasPrefix(t, "-o") && !strings.HasPrefix(t, "--") && len(t) > 2:
			v = strings.TrimPrefix(t[2:], "=")
		default:
			continue
		}
		format, _, _ := strings.Cut(v, "=")
		format = strings.ToLower(format)
		if strings.HasSuffix(format, "-file") || format == "templatefile" {
			return fmt.Errorf("kubectl: output format %q reads a local file and is blocked; pass the template inline (e.g. -o jsonpath={...})", format)
		}
	}
	return nil
}

// mountedSecretPathIn returns the mounted-secret path a token names, or "".
// The value part of a token is cleaned too, so `/var/run/../run/secrets` and
// `--token-file=/var/./run/secrets/x` still match.
func mountedSecretPathIn(tokens []string) string {
	for _, t := range tokens {
		candidates := []string{strings.ToLower(t)}
		if strings.Contains(t, "/") {
			_, v, hasEq := strings.Cut(t, "=")
			if !hasEq {
				v = t
			}
			candidates = append(candidates, strings.ToLower(path.Clean(v)))
		}
		for _, c := range candidates {
			for _, p := range mountedSecretPaths {
				if strings.Contains(c, p) {
					return p
				}
			}
		}
	}
	return ""
}

func contains(tokens []string, want string) bool {
	for _, t := range tokens {
		if strings.ToLower(t) == want {
			return true
		}
	}
	return false
}

// IsSecretBearingKind reports whether a kind or resource name, in any spelling
// kubectl or the API accepts (`Secret`, `secrets`, `secrets.v1`, `es`), is one
// whose objects hold credentials. For callers that address resources by
// name rather than through a kubectl command (the agent's get_resource).
func IsSecretBearingKind(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	n, _, _ = strings.Cut(n, "/")
	n, _, _ = strings.Cut(n, ".")
	return secretBearingKinds[n]
}
