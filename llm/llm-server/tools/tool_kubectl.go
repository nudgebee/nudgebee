package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"

	"github.com/pkg/errors"
)

const ToolExecuteKubectlCommand = "kubectl_execute"

// kubectlStderrNoisePrefixes are kubectl stderr lines that the workspace pod's
// /execute handler merges into stdout (because cmd.Stdout and cmd.Stderr point at
// the same buffer). They are informational notices, not actual command output, and
// must be stripped before the result is shown to the agent — otherwise the LLM
// interprets them as the only output and concludes the command failed.
var kubectlStderrNoisePrefixes = []string{
	`Defaulted container "`, // multi-container pod, no -c flag
	"Warning: ",             // deprecation / version warnings
	"W0",                    // klog warning lines (e.g. W0406 ...)
	"I0",                    // klog info lines (e.g. I0406 ...)
	"E0",                    // klog error lines (e.g. E0406 ...)
	"Flag --",               // deprecated flag notices
	"Unable to use a TTY",   // exec without TTY notice
}

// splitKubectlStderrNoise separates a leading prefix of kubectl stderr notice
// lines from the real stdout in a merged stdout/stderr blob. The workspace
// /execute handler points cmd.Stdout and cmd.Stderr at the same buffer, so
// without splitting we'd either lose the notices (silent stderr) or mistake
// them for the only output (breaks "empty grep result is the correct answer").
func splitKubectlStderrNoise(response string) (stdout, stderr string) {
	if response == "" {
		return "", ""
	}
	lines := strings.Split(response, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]
		matched := false
		for _, prefix := range kubectlStderrNoisePrefixes {
			if strings.HasPrefix(line, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			break
		}
		i++
	}
	if i == 0 {
		return response, ""
	}
	return strings.Join(lines[i:], "\n"), strings.Join(lines[:i], "\n")
}

func init() {
	core.RegisterNBToolFactory(ToolExecuteKubectlCommand, func(accountId string) (core.NBTool, error) {
		return KubectlExecuteTool{}, nil
	})
}

// kubectlKindAliases maps kubectl resource type tokens (singular, plural, and
// short names) to the canonical UI tab id used in /kubernetes/details fragment
// routing. Only resource kinds that have a dedicated UI tab are listed;
// anything else falls back to the generic Applications tab.
var kubectlKindAliases = map[string]string{
	"pod": "pods", "pods": "pods", "po": "pods",
	"service": "services", "services": "services", "svc": "services",
	"namespace": "namespaces", "namespaces": "namespaces", "ns": "namespaces",
	"persistentvolumeclaim": "pvc", "persistentvolumeclaims": "pvc", "pvc": "pvc",
	"persistentvolume": "pv", "persistentvolumes": "pv", "pv": "pv",
	"node": "nodes", "nodes": "nodes", "no": "nodes",
}

// kubectlKindVerbs are kubectl subcommands whose first positional argument is
// a resource type (e.g. `kubectl get pods`, `kubectl describe pvc/foo`).
var kubectlKindVerbs = map[string]bool{
	"get": true, "describe": true, "top": true, "delete": true,
	"edit": true, "scale": true, "rollout": true, "set": true,
	"wait": true, "label": true, "annotate": true, "patch": true,
	"explain": true,
}

// kubectlPodVerbs are kubectl subcommands that operate exclusively on pods.
// The next positional token is a pod name, not a resource kind.
var kubectlPodVerbs = map[string]bool{
	"logs": true, "exec": true, "port-forward": true, "attach": true, "cp": true,
}

// kubectlBlockedResourceKinds is the set of resource kinds whose contents
// hold credentials and must never be readable, writable, or deletable via
// the kubectl tool — regardless of verb. The same tokenizer rules
// kubectlResourceKind applies (slash-form `kind/name`, FQDN `kind.group`,
// comma list `kind1,kind2`) are used to normalize incoming tokens before
// matching this map.
// Short names are the official kubectl aliases declared in each CRD's
// spec.names.shortNames (External-Secrets-Operator and Secrets-Store CSI
// driver). `ss` is SecretStore — not statefulsets, which use `sts`.
var kubectlBlockedResourceKinds = map[string]bool{
	"secret": true, "secrets": true,
	"sealedsecret": true, "sealedsecrets": true,
	"externalsecret": true, "externalsecrets": true, "es": true,
	"secretproviderclass": true, "secretproviderclasses": true, "spc": true, "spcs": true,
	"clustersecretstore": true, "clustersecretstores": true, "css": true,
	"secretstore": true, "secretstores": true, "ss": true,
}

// kubectlBlockResourceVerbs are the subcommands we scan for a blocked
// resource kind after. We intentionally include both read verbs
// (get/describe/...) and mutation verbs (create/delete/edit/apply/...).
// Pod-only verbs (exec/logs/...) are NOT included here because their
// positional is a pod name; secret-via-mounted-filesystem reads in those
// commands are caught by kubectlReadsSecretFilesystemPath instead.
var kubectlBlockResourceVerbs = map[string]bool{
	"get": true, "describe": true, "top": true, "explain": true, "wait": true,
	"create": true, "delete": true, "edit": true, "apply": true, "replace": true,
	"scale": true, "rollout": true, "set": true,
	"label": true, "annotate": true, "patch": true,
}

// kubectlSecretFilesystemPatterns lists the in-pod paths where kubelet
// mounts Service Account tokens and Secret volumes. An `exec`/`cp`/`attach`
// that touches any of these is treated as a secret-data read regardless of
// the kubectl-level kind. No trailing slash so we match both
// `/run/secrets/foo` (subpath form) and `/run/secrets` (whole-dir form, as
// in `find /run/secrets -type f`). `kubernetes.io/serviceaccount` is
// included as defense-in-depth so relative-path traversals like
// `cd /var/run && cat secrets/kubernetes.io/serviceaccount/token` are
// still caught.
var kubectlSecretFilesystemPatterns = []string{
	"/var/run/secrets",
	"/var/lib/kubelet/pods",
	"/run/secrets",
	"kubernetes.io/serviceaccount",
}

// shellQuoteStripper removes characters that the shell evaluates to
// nothing (single/double quotes, backslashes). Without this,
// `sec""ret`, `'secret'`, and `s\e\c\r\e\t` would bypass the
// exact-token denylist while still being executed as `secret` by the
// shell on the workspace pod.
var shellQuoteStripper = strings.NewReplacer(
	"\"", "",
	"'", "",
	"\\", "",
)

// shellMetacharNormalizer turns shell command-separator and
// substitution metacharacters into spaces so they don't get glued
// onto a kind token by Fields-based tokenization. Without this,
// `kubectl get secret;`, `kubectl get secret|grep foo`, and
// `kubectl get $(echo secret)` would produce tokens like `secret;`,
// `secret|grep`, and `secret)` that fall through the exact-match
// denylist while still being executed as `secret` by the shell.
var shellMetacharNormalizer = strings.NewReplacer(
	";", " ",
	"&", " ",
	"|", " ",
	">", " ",
	"<", " ",
	"(", " ",
	")", " ",
	"`", " ",
	"$", " ",
)

// kubectlBlockedKind walks a kubectl command and returns the first blocked
// resource kind found (or "" if none). Mirrors the tokenizer rules in
// kubectlResourceKind: positional after a known verb, scan-don't-break on
// non-flag tokens to handle `-o yaml secret` (flag values sit between the
// verb and the kind). Each candidate is normalized by splitting on `,`
// FIRST (so `pods/foo,secrets/bar` correctly surfaces `secrets`), then
// per-segment stripping `/<name>` and `.<group>`. The whole command is
// preprocessed via shellQuoteStripper (so `"secret"` and `s\e\c\r\e\t`
// match) and shellMetacharNormalizer (so `secret;`, `secret|grep`, and
// `$(echo secret)` produce isolated `secret` tokens).
//
// Known limitation: we don't track which short flags take a value, so a
// pod named exactly `secret` / `secrets` in `kubectl get pod secret` is
// falsely blocked. Pod names that *contain* `secret` as a substring
// (`secret-rotator`, `my-secret-pod`) are NOT falsely blocked because the
// match is exact-token against the denylist map.
func kubectlBlockedKind(command string) string {
	cmd := strings.TrimSpace(command)
	cmd = strings.TrimPrefix(cmd, "kubectl")
	cmd = shellQuoteStripper.Replace(cmd)
	cmd = shellMetacharNormalizer.Replace(cmd)
	tokens := strings.Fields(cmd)

	for i, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		verb := strings.ToLower(tok)
		if !kubectlBlockResourceVerbs[verb] {
			continue
		}
		for j := i + 1; j < len(tokens); j++ {
			next := tokens[j]
			if strings.HasPrefix(next, "-") {
				continue
			}
			// Split on `,` FIRST so a list like `pods/foo,secrets/bar`
			// is processed segment-by-segment — otherwise the `/`
			// strip on the whole token would discard everything after
			// the first `/` and miss `secrets/bar`.
			for _, segment := range strings.Split(next, ",") {
				segment = strings.SplitN(segment, "/", 2)[0]
				segment = strings.SplitN(segment, ".", 2)[0]
				if kubectlBlockedResourceKinds[strings.ToLower(segment)] {
					return strings.ToLower(segment)
				}
			}
		}
		return ""
	}
	return ""
}

// kubectlReadsSecretFilesystemPath returns true if the command uses a
// pod-filesystem verb (exec / cp / attach) and references one of the
// well-known kubelet-mounted secret paths. Catches commands like
//
//	kubectl exec mypod -- cat /var/run/secrets/kubernetes.io/serviceaccount/token
//	kubectl cp mypod:/var/run/secrets/foo /tmp/x
//	kubectl exec mypod -- sh -c "find /run/secrets -type f"
//
// The substring check on the path is necessary because the part of the
// command after `--` is an arbitrary shell command, not kubectl args.
// shellQuoteStripper is applied first so `/var/run/sec""rets` or
// `/var/run/se\crets` (which the shell evaluates to `/var/run/secrets`)
// can't bypass the literal substring check.
func kubectlReadsSecretFilesystemPath(command string) bool {
	cmd := shellQuoteStripper.Replace(command)
	tokens := strings.Fields(strings.TrimPrefix(strings.TrimSpace(cmd), "kubectl"))
	hasFilesystemVerb := false
	for _, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		verb := strings.ToLower(tok)
		if verb == "exec" || verb == "cp" || verb == "attach" {
			hasFilesystemVerb = true
			break
		}
	}
	if !hasFilesystemVerb {
		return false
	}
	for _, pat := range kubectlSecretFilesystemPatterns {
		if strings.Contains(cmd, pat) {
			return true
		}
	}
	return false
}

// kubectlResourceKind inspects a kubectl command string and returns the
// canonical UI resource kind it targets (one of pods, services, namespaces,
// pvc, pv, nodes). Returns "" when the kind cannot be determined or maps to a
// workload/other kind that belongs in the generic Applications tab.
func kubectlResourceKind(command string) string {
	cmd := strings.TrimSpace(command)
	cmd = strings.TrimPrefix(cmd, "kubectl")
	tokens := strings.Fields(cmd)

	for i, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		verb := strings.ToLower(tok)
		if kubectlPodVerbs[verb] {
			return "pods"
		}
		if !kubectlKindVerbs[verb] {
			continue
		}
		// Scan the remaining tokens for the first one that matches a known
		// resource kind. We don't stop at the first non-flag token because
		// flag arguments can sit between the verb and the kind (e.g.
		// `kubectl get -o yaml pvc -n bar`), and we don't track which short
		// flags take a value.
		for j := i + 1; j < len(tokens); j++ {
			next := tokens[j]
			if strings.HasPrefix(next, "-") {
				continue
			}
			// Strip a resource-name suffix (`pvc/foo` -> `pvc`) and a
			// comma list (`po,svc` -> `po`) — when multiple kinds are
			// requested only the first one drives the UI destination.
			next = strings.SplitN(next, "/", 2)[0]
			next = strings.SplitN(next, ",", 2)[0]
			if kind, ok := kubectlKindAliases[strings.ToLower(next)]; ok {
				return kind
			}
		}
		return ""
	}
	return ""
}

// kubectlUIRef builds the NBToolResponseReference attached to a kubectl tool
// response. It derives the UI tab fragment from the kubectl command so the
// source link lands on the resource-specific tab (pods, pvc, etc.) rather
// than always pointing at the Applications tab, and pre-filters that tab to
// the command's namespace when one is given (the k8s resource tabs read
// ?namespace=<ns>). Without this the link lands on an unfiltered list of
// every resource across all namespaces.
func kubectlUIRef(ctx core.NbToolContext, command string) core.NBToolResponseReference {
	modules, label := kubectlUIReference(command)
	var queryParams map[string]string
	if ns := kubectlNamespace(command); ns != "" {
		queryParams = map[string]string{"namespace": ns}
	}
	return core.GetNudgebeeUIReferenceForClusterDetails(ctx, modules, label, queryParams, "")
}

// kubectlNamespace extracts the namespace a kubectl command targets. It
// recognises `-n <ns>`, `-n=<ns>`, `--namespace <ns>` and `--namespace=<ns>`.
// It returns "" when no namespace is specified or when the command spans all
// namespaces (`-A` / `--all-namespaces`) — in both cases the UI tab should
// stay unfiltered rather than guess.
func kubectlNamespace(command string) string {
	tokens := strings.Fields(command)
	for i, tok := range tokens {
		if tok == "-A" || tok == "--all-namespaces" {
			return ""
		}
		switch {
		case tok == "-n" || tok == "--namespace":
			if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
				return strings.Trim(tokens[i+1], `"'`)
			}
		case strings.HasPrefix(tok, "--namespace="):
			return strings.Trim(strings.TrimPrefix(tok, "--namespace="), `"'`)
		case strings.HasPrefix(tok, "-n="):
			return strings.Trim(strings.TrimPrefix(tok, "-n="), `"'`)
		}
	}
	return ""
}

// kubectlUIReference returns the (modules, label) pair used by kubectlUIRef.
// Exposed separately so it can be unit-tested without a NbToolContext.
func kubectlUIReference(command string) ([]string, string) {
	switch kubectlResourceKind(command) {
	case "pods":
		return []string{"kubernetes", "pods"}, "View Pods"
	case "services":
		return []string{"kubernetes", "services"}, "View Services"
	case "namespaces":
		return []string{"kubernetes", "namespaces"}, "View Namespaces"
	case "pvc":
		return []string{"kubernetes", "pvc"}, "View PVCs"
	case "pv":
		return []string{"kubernetes", "pv"}, "View PVs"
	case "nodes":
		return []string{"kubernetes", "nodes"}, "View Nodes"
	default:
		return []string{"kubernetes", "applications"}, "Check Apps & Pods"
	}
}

type KubectlExecuteTool struct {
}

func (m KubectlExecuteTool) Name() string {
	return ToolExecuteKubectlCommand
}

func (m KubectlExecuteTool) GetNameAliases() []string {
	return []string{"kubectl"}
}

func (m KubectlExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m KubectlExecuteTool) Description() string {
	return `Executes 'kubectl' commands against the user's Kubernetes cluster. This tool allows you to gather information about the cluster's resources and configuration, enabling you to provide informed assistance and suggestions.

		**Usage:**

		* **Prioritize this tool:** Whenever you require information about the user's cluster to make decisions or provide accurate responses, use this tool.
		* **Input:** Provide a valid, 'kubectl' command as input. Shell piping (|) is supported — you can pipe kubectl output through grep, head, tail, awk, etc.
		* **Output:** The tool will return the output of the executed command.

		**Examples:**

		* 'kubectl get pods'
		* 'kubectl describe node <node-name>'
		* 'kubectl get events --sort-by=.metadata.creationTimestamp'
		* 'kubectl get pods -A -o "custom-columns=NAME:.metadata.name,NAMESPACE:.metadata.namespace"'

		**Log Commands — IMPORTANT:**

		When fetching logs, ALWAYS use --tail or --since to limit output. Unfiltered logs can return hundreds of thousands of lines and overwhelm the response.
		Combine --tail with grep/head/tail pipes to get focused, relevant output.

		* 'kubectl logs <pod> -n <namespace> --tail 200' — limit to recent lines
		* 'kubectl logs <pod> -n <namespace> --tail 500 | grep -i -E "(error|exception|fatal|panic|fail|warn)"' — filter for errors
		* 'kubectl logs <pod> -n <namespace> --since=1h | grep -i error' — recent logs with error filter
		* 'kubectl logs <pod> -n <namespace> --tail 1000 | grep -i -E "(error|exception)" | head -50' — cap filtered output
		* 'kubectl logs <pod> -n <namespace> --tail 500 | grep -i -B2 -A2 error' — errors with surrounding context
		* 'kubectl logs <pod> -n <namespace> --tail 500 | awk "/error|exception/,/^$/"' — extract error blocks
		* 'kubectl logs <pod> -n <namespace> -p --tail 200' — previous container logs (crash loops)
		* 'kubectl logs <pod> -n <namespace> --tail 500 | tail -100' — last 100 lines of recent 500

		**Important Notes:**

		* Ensure the 'kubectl' command is correctly formatted.
		* Whenever possible, provide namespace
		* **Quoting Arguments:** Always wrap complex arguments, especially those with special characters (e.g., '-o custom-columns=...', '-o jsonpath=...', '-l', '--field-selector', '[', '(', '?', '@', '*'), in double or single quotes to ensure correct execution in the shell.
		* Use the output of this tool to inform your responses and suggestions to the user.
		`
}

func (m KubectlExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Kubectl command to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m KubectlExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", m.Name())
	}

	nbRequestContext.Ctx.GetLogger().Info("k8s: executing executeShellCommand tool call", "query", input.Command)
	command := strings.TrimSpace(input.Command)
	if !strings.HasPrefix(command, "kubectl") {
		command = "kubectl " + command
	}

	command1 := strings.ToLower(command)
	// Block secret-bearing resource kinds. Uses a shell-aware tokenizer
	// (kubectlBlockedKind) instead of a substring check so that
	// `kubectl get secret/my-tls -o yaml`, `kubectl get -o yaml secret X`,
	// `kubectl get secrets.v1.core`, `kubectl get pods,secrets`, and
	// `kubectl get sealedsecrets.bitnami.com/foo` are all caught.
	if blocked := kubectlBlockedKind(command); blocked != "" {
		return core.NBToolResponse{}, fmt.Errorf("kubectl: access to %q is blocked. Secret-bearing kinds (secrets, sealedsecrets, externalsecrets, secretstores, secretproviderclasses) are not readable via this tool", blocked)
	}
	// Block exec/cp/attach reads from well-known secret mount paths so
	// `kubectl exec mypod -- cat /var/run/secrets/...` can't bypass the
	// kind-level check above.
	if kubectlReadsSecretFilesystemPath(command) {
		return core.NBToolResponse{}, errors.New("kubectl: reading mounted secret filesystem paths (/var/run/secrets, /var/lib/kubelet/pods, /run/secrets) via exec/cp/attach is blocked")
	}

	// Safety net: auto-inject --tail for kubectl logs if not already limited
	if strings.Contains(command1, " logs ") || strings.HasPrefix(command1, "kubectl logs") {
		if !strings.Contains(command, "--tail") && !strings.Contains(command, "--since") && !strings.Contains(command, " -f") {
			// Insert --tail before any pipe to avoid breaking piped commands
			if pipeIdx := strings.Index(command, "|"); pipeIdx > 0 {
				command = strings.TrimRight(command[:pipeIdx], " ") + " --tail 500 | " + strings.TrimLeft(command[pipeIdx+1:], " ")
			} else {
				command += " --tail 500"
			}
		}
	}

	// we want to ignor eany context related information as system always uses default context
	if strings.Contains(command, "--context") {
		parts := strings.Fields(command)
		for i, p := range parts {
			if p == "--context" {
				// Ensure there is an argument after --context before slicing
				if i+1 < len(parts) {
					parts = append(parts[:i], parts[i+2:]...)
				} else {
					// --context is the last argument, just remove it
					parts = parts[:i]
				}
				command = strings.Join(parts, " ")
				break
			}
		}
	}

	// Extract accountId from tool config (selected cluster account)
	configAccountId := ""
	for _, v := range nbRequestContext.ToolConfig.Values {
		if v.Name == "id" {
			configAccountId = v.Value
			break
		}
	}

	// Use config-selected account if available, otherwise fall back to request account
	effectiveAccountId := nbRequestContext.AccountId
	if configAccountId != "" {
		effectiveAccountId = configAccountId
	}

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()
		response, err := wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, effectiveAccountId, nbRequestContext.ConversationId, command, map[string]string{
			workspace.ENV_NB_TOOL_CONFIG_NAME: nbRequestContext.ToolConfig.Name,
		})
		if err != nil {
			// Pipeline-tail no-match reclassification (issue #32240).
			// The LLM regularly uses kubectl with `| grep` / `| awk` /
			// `| jq` to filter output (`kubectl get pods | grep api-server`).
			// When the filter finds nothing the pipeline exits 1, the
			// workspace reports "exit status 1", and the LLM sees an
			// opaque failure for what was a successful empty result. The
			// helpers from tool_shell.go (PR #32007) already classify
			// these correctly; we just need to wire them in.
			if isNoMatchExit(err, command) {
				nbRequestContext.Ctx.GetLogger().Info("k8s: reclassified pipeline-tail no-match as success", "command", command)
				return successResponseNoMatches(nbRequestContext, response)
			}
			nbRequestContext.Ctx.GetLogger().Error("k8s: unable to execute shell script", "error", err.Error(), "command", command)
			if response == "" {
				response = err.Error()
			}
			return core.NBToolResponse{
				Data:   response,
				Status: core.NBToolResponseStatusError,
			}, err
		}

		stdout, stderr := splitKubectlStderrNoise(response)

		// Wrap stdout in JSON for consistency with non-workspace mode and to
		// let agents parse it. Stderr is intentionally NOT packed into this
		// envelope — it travels via Metadata.Stderr so it stays out of the
		// observation text the UI renders (and isn't tool-specific anymore).
		outputformat := map[string]string{
			"stdout": stdout,
		}
		outputformatBytes, err := common.MarshalJson(outputformat)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("kubectl: unable to marshal response", "error", err.Error())
			return core.NBToolResponse{
				Data:   response,
				Status: core.NBToolResponseStatusError,
			}, err
		}
		response = string(outputformatBytes)

		resp := core.NBToolResponse{
			Data:       response,
			Type:       core.NBToolResponseTypeText,
			Status:     core.NBToolResponseStatusSuccess,
			References: []core.NBToolResponseReference{kubectlUIRef(nbRequestContext, command)},
		}
		if stderr != "" {
			resp.Metadata = &core.NBToolResponseMetadata{Stderr: stderr}
		}
		return resp, nil
	}

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobKubectl, command, effectiveAccountId, map[string]any{}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("k8s: unable to execute shell script", "error", err.Error(), "command", command)
		// ExecuteContainerJob returns (nil, err) on every failure path
		// (relay.Execute error, getRelayCommandResponseData parser error,
		// downstream unmarshal failure), so `response` is always nil here
		// and the only thing that carries the actual signal — including
		// the shim's "Error: Server returned NNN: ..." wrapper that the
		// hint patterns are tuned for — is err.Error(). Use it as the
		// raw input to wrapKubectlError. The `response` assertion below
		// is defensive in case a future code path starts returning a
		// non-nil response alongside an error.
		rawError := err.Error()
		if response != nil {
			if responseStr, ok := response.(string); ok && responseStr != "" {
				rawError = responseStr
			}
		}
		return core.NBToolResponse{
			Data:   wrapKubectlError(rawError, command),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	data := response.(string)
	resp := core.NBToolResponse{
		Data:       data,
		Type:       core.NBToolResponseTypeText,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{kubectlUIRef(nbRequestContext, command)},
	}

	return resp, nil
}

// wrapKubectlError mirrors tool_shell's wrapShellError for kubectl-side
// failures. When the raw response matches a known opaque-failure pattern
// (the shim's `Error: Server returned NNN: ...` wrapper that the LLM
// otherwise sees as an unactionable "infrastructure broken" signal), we
// emit a structured {"error_hint": ..., "original_error": ...} envelope
// so the LLM gets something to act on. Raw stderr is preserved verbatim
// under original_error. Pass-through unchanged when no pattern matches.
//
// Pattern coverage today (driven by the 14-day error distribution in
// issue #32240):
//   - "Server returned 500: ...status: 400 Bad Request..." — the kubectl
//     command was syntactically valid as a Go string but the workspace
//     pod rejected it. Hint points at common kubectl bad-command shapes.
//   - "Server returned 500: ...findings field not found..." — defensive
//     after-the-fact coverage. The parser fix in
//     getRelayCommandResponseData removes the dominant source of this,
//     but we keep the hint in case another code path produces it.
func wrapKubectlError(rawError, command string) string {
	if rawError == "" {
		return rawError
	}
	hint := kubectlErrorHint(rawError)
	if hint == "" {
		return rawError
	}
	envelope := map[string]string{
		"error_hint":     hint,
		"original_error": rawError,
	}
	body, err := common.MarshalJson(envelope)
	if err != nil {
		// Marshal failure is exceptionally unlikely with two string fields;
		// fall back to raw so the LLM still sees the underlying signal.
		return rawError
	}
	return string(body)
}

// kubectlErrorHint maps a raw kubectl error string to an actionable hint
// for the LLM. Returns "" when no pattern matches — the caller then
// passes the raw error through unchanged.
func kubectlErrorHint(rawError string) string {
	lower := strings.ToLower(rawError)
	switch {
	case strings.Contains(lower, "status: 400 bad request"):
		return "The workspace pod rejected this kubectl command as malformed (HTTP 400). Common causes: missing `-n <namespace>` flag, invalid resource type (e.g. `pos` instead of `pods`), bad `--field-selector` syntax, or an unknown subresource. Try `kubectl explain <resource>` to verify field names, `kubectl <verb> --dry-run=client -o yaml` to validate the command shape, and add `-v=6` for more detail on what the API server saw."
	case strings.Contains(lower, "findings field not found"):
		// Decoupled from any "server returned 500" wrapper so the hint
		// fires whether the error reaches us via the shim ("Error:
		// Server returned 500: {...findings field not found...}") or
		// directly from a parser (errors.New("findings field not found
		// or is nil from data")) — see PR #32243 Gemini review.
		return "The relay-server returned a response shape the parser didn't recognize. This was the dominant failure mode pre-#32240; if you are seeing it post-fix the relay or workspace pod is returning an unexpected payload — retry once, then fall back to a `resource_search` to confirm the resource exists."
	}
	return ""
}

func (m KubectlExecuteTool) IdentifyConfig(ctx core.NbToolContext, input core.NBToolCallRequest, availableConfigs []core.ToolConfig) (core.ToolConfig, error) {
	// 1. Try to match via context labels (e.g. nb_cloud_account_id from UI context)
	if ctx.QueryConfig.Labels != nil {
		var cloudAccountID string
		if val, ok := ctx.QueryConfig.Labels["nb_cloud_account_id"].(string); ok {
			cloudAccountID = val
		}

		if cloudAccountID != "" {
			for _, cfg := range availableConfigs {
				for _, v := range cfg.Values {
					if v.Name == "account_number" && v.Value == cloudAccountID {
						return cfg, nil
					}
					if v.Name == "id" && v.Value == cloudAccountID {
						return cfg, nil
					}
				}
			}
		}
	}

	// 2. If project/cluster ID is mentioned in the command, try to find matching config
	command := strings.ToLower(input.Command)
	var matches []core.ToolConfig

	for _, cfg := range availableConfigs {
		matched := false
		// Cluster ID is often used in names
		if cfg.Name != "" && len(cfg.Name) >= 3 && strings.Contains(command, strings.ToLower(cfg.Name)) {
			matched = true
		}

		if !matched {
			// Check values for id, cluster_id, etc.
			for _, v := range cfg.Values {
				lowName := strings.ToLower(v.Name)
				if (lowName == "id" || lowName == "cluster_id" || lowName == "cluster_name" ||
					lowName == "account_id" || lowName == "account_number" || lowName == "name") && len(v.Value) >= 3 {
					if strings.Contains(command, strings.ToLower(v.Value)) {
						matched = true
						break
					}
				}
			}
		}

		if matched {
			matches = append(matches, cfg)
		}
	}

	// Ambiguous: multiple configs matched — let the next strategy decide
	if len(matches) != 1 {
		return core.ToolConfig{}, nil
	}
	return matches[0], nil
}

func (m KubectlExecuteTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are a Kubernetes security expert. Your task is to classify an input string as a 'kubectl' command type.

	The input might be a full 'kubectl' command or a JSONPath expression used for filtering output.

	Based on the input, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	command - kubectl run pod1 --image=ubuntu --restart=Never -- sleep 3600
	answer - write
	reason - command is creating pod

	command - kubectl get pods --all-namespaces
	answer - read
	reason - command is reading pod details

	command - kubectl apply -f pod.yaml
	answer - write
	reason - command is updating resources in cluster

	command - kubectl describe pod pod1
	answer - read
	reason - command is reading pod details

	command - kubectl scale deployment my-deployment --replicas=3
	answer - write
	reason - command is updating resources in cluster

	command - kubectl edit deployment my-deployment
	answer - write
	reason - command is updating resources in cluster

	command - kubectl get nodes
	answer - read
	reason - command is reading node details

	command - kubectl top pods
	answer - read
	reason - command is reading pod resource usage details

	command - kubectl config view
	answer - read
	reason - command is reading kubeconfig details

	command - kubectl cordon node1
	answer - write
	reason - command is updating node details

	`
	return prompt, nil
}

func (m KubectlExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"account_name", "account_number"},
		ConfigType:   "k8s",
		ConfigSource: core.ToolConfigSourceAccount,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}
