package relay

// Kubectl secret-access policy for RelayJobKubectl. A copy of api-server
// services/kubectlpolicy/policy.go (runbook-server is a separate Go module);
// both mirror llm-server tools.ValidateKubectlRelayCommand and the agent's own
// check in k8s-agent runner/pkg/kube/secretpolicy.go. Keep them in step.
//
// It runs on every kubectl command an automation sends, whether the user typed
// it, an alert filled it in, or a model wrote it. The agent refuses the same
// commands once upgraded; this covers agents that are not. It is a denylist,
// not a parser: when in doubt it refuses.

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// kubectlSecretKinds are resource kinds whose objects hold credentials, in
// every spelling kubectl accepts: singular, plural and the short names the
// External Secrets Operator and Secrets Store CSI CRDs declare. `ss` is
// SecretStore; statefulsets are `sts`.
var kubectlSecretKinds = map[string]bool{
	"secret": true, "secrets": true,
	"sealedsecret": true, "sealedsecrets": true,
	"externalsecret": true, "externalsecrets": true, "es": true,
	"secretproviderclass": true, "secretproviderclasses": true, "spc": true, "spcs": true,
	"clustersecretstore": true, "clustersecretstores": true, "css": true,
	"secretstore": true, "secretstores": true, "ss": true,
}

// kubectlSecretAPIResources are the same kinds as an API path names them
// (`--raw /api/v1/namespaces/x/secrets`). The API server routes only singular
// and plural names, so the short names are left out here.
var kubectlSecretAPIResources = map[string]bool{
	"secret": true, "secrets": true,
	"sealedsecret": true, "sealedsecrets": true,
	"externalsecret": true, "externalsecrets": true,
	"secretproviderclass": true, "secretproviderclasses": true,
	"clustersecretstore": true, "clustersecretstores": true,
	"secretstore": true, "secretstores": true,
}

// kubectlResourceVerbs are the subcommands whose positional arguments name
// resource kinds. Write verbs are included: Secrets are not created, edited or
// deleted through this path either.
var kubectlResourceVerbs = map[string]bool{
	"get": true, "describe": true, "top": true, "explain": true, "wait": true,
	"create": true, "delete": true, "edit": true, "apply": true, "replace": true,
	"scale": true, "rollout": true, "set": true,
	"label": true, "annotate": true, "patch": true,
}

// kubectlMountedSecretPaths are where kubelet mounts ServiceAccount tokens and Secret
// volumes. No leading slash, so relative spellings match too.
var kubectlMountedSecretPaths = []string{
	"var/run/secrets",
	"run/secrets",
	"var/lib/kubelet/pods",
	"kubernetes.io/serviceaccount",
}

// kubectlQuoteStripper removes what a shell or shlex reads as nothing, so `sec""ret`,
// `'secret'` and `s\ecret` are seen as `secret`.
var kubectlQuoteStripper = strings.NewReplacer(`"`, "", "'", "", `\`, "")

// kubectlMetacharSplitter turns shell separators into spaces so `secret;` or
// `$(echo secret)` yield a bare `secret` token.
var kubectlMetacharSplitter = strings.NewReplacer(
	";", " ", "&", " ", "|", " ", ">", " ", "<", " ",
	"(", " ", ")", " ", "`", " ", "$", " ", "\n", " ", "\r", " ",
)

// checkKubectlSecretAccess returns an error when command would reach a Secret or a
// Secret-like resource, a mounted credential file, or mint a token.
func checkKubectlSecretAccess(command string) error {
	tokens := kubectlTokenize(command)
	if err := kubectlCheckRawPaths(tokens); err != nil {
		return err
	}
	if err := kubectlCheckOutputFormats(tokens); err != nil {
		return err
	}
	if p := kubectlMountedSecretPathIn(tokens); p != "" {
		return fmt.Errorf("kubectl: reading mounted secret paths (%s) is blocked", p)
	}
	verbAt := -1
	for i, t := range tokens {
		if !strings.HasPrefix(t, "-") && kubectlResourceVerbs[strings.ToLower(t)] {
			verbAt = i
			break
		}
	}
	if verbAt < 0 {
		return nil
	}
	if strings.ToLower(tokens[verbAt]) == "create" && kubectlContainsToken(tokens[verbAt+1:], "token") {
		return errors.New("kubectl: minting ServiceAccount tokens (create token) is blocked")
	}
	if kind := kubectlSecretKindIn(tokens[verbAt+1:]); kind != "" {
		return fmt.Errorf("kubectl: access to %q is blocked. Secret-bearing kinds (secrets, sealedsecrets, externalsecrets, secretstores, secretproviderclasses) are not readable through Nudgebee", kind)
	}
	return nil
}

func kubectlTokenize(command string) []string {
	cmd := kubectlQuoteStripper.Replace(command)
	cmd = kubectlMetacharSplitter.Replace(cmd)
	tokens := strings.Fields(cmd)
	if len(tokens) > 0 && (tokens[0] == "kubectl" || strings.HasSuffix(tokens[0], "/kubectl")) {
		tokens = tokens[1:]
	}
	return tokens
}

// kubectlSecretKindIn returns the first secret-bearing kind named by a token
// after the verb, or "". Positional tokens and the value of `--flag=value`
// (`--from=secret/x`) are checked. Each is split on `,` first (`pods,secrets`),
// then stripped of `/name` and `.group` (`secret/x`, `secrets.v1.`).
//
// Values of space-separated flags are checked as positionals, since which flags
// take a value is not tracked: a namespace named `secrets` or `es` is refused.
func kubectlSecretKindIn(tokens []string) string {
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
			if kubectlSecretKinds[part] {
				return part
			}
		}
	}
	return ""
}

// kubectlCheckRawPaths refuses `--raw PATH` when PATH addresses a secret-bearing
// resource or ServiceAccount token minting. The path is percent-decoded until
// stable, lowercased and cleaned, since the API server decodes it before
// routing (`%73ecrets` reaches the secrets handler).
func kubectlCheckRawPaths(tokens []string) error {
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
				if kubectlSecretAPIResources[s] {
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

// kubectlCheckOutputFormats refuses file-backed output formats (`-o jsonpath-file=`,
// `go-template-file`, `custom-columns-file`, `templatefile`). kubectl reads the
// file on the agent pod as a template and echoes it on a parse error, the
// agent's own ServiceAccount token included.
func kubectlCheckOutputFormats(tokens []string) error {
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

// kubectlMountedSecretPathIn returns the mounted-secret path a token names, or "".
// The value part of a token is cleaned too, so `/var/run/../run/secrets` and
// `--token-file=/var/./run/secrets/x` still match.
func kubectlMountedSecretPathIn(tokens []string) string {
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
			for _, p := range kubectlMountedSecretPaths {
				if strings.Contains(c, p) {
					return p
				}
			}
		}
	}
	return ""
}

func kubectlContainsToken(tokens []string, want string) bool {
	for _, t := range tokens {
		if strings.ToLower(t) == want {
			return true
		}
	}
	return false
}
