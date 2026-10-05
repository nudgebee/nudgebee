package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/shlex"
	"gopkg.in/yaml.v3"
)

// Draft plan -> read the live state it depends on -> one repair against it. Why & rejected
// alternatives: docs/architecture-decisions.md, "A remediation plan is checked against the live state".

const (
	targetReadBudget   = 20 * time.Second
	targetReadMaxBytes = 4000
	targetReadMaxCount = 8
)

// targetRead is one read of something the draft plan changes.
type targetRead struct {
	Target  string `json:"target"`
	Source  string `json:"source"`
	Content string `json:"content,omitempty"`
	Unknown string `json:"unknown,omitempty"`
}

// targetSpec is a read to perform: a read-only command through the account's executor, or a lookup
// of key names in the knowledge graph (Secret and ConfigMap values never enter llm-server).
type targetSpec struct {
	Target  string
	Command string
	KGKey   string
	// Unresolved says why a read was not performed; such a read is reported, not run.
	Unresolved string
}

// targetReader performs the reads; a seam for tests.
var targetReader = readTargets

// stateReadRequest is a read-only command the draft asks NudgeBee to run before the plan is shown,
// with the concern it bears on. It is checked like any read (readRejection) and never trusted for being one.
type stateReadRequest struct {
	Argv    []string `json:"argv"`
	Concern string   `json:"concern,omitempty"`
}

const stateReadMaxRequests = 4

// draftStateReads extracts the draft's read requests. They are kept off RemediationPlan, so they never
// reach the stored plan, the panel, or the draft the repair pass is shown.
func draftStateReads(raw string) []stateReadRequest {
	var side struct {
		StateReads []stateReadRequest `json:"state_reads"`
	}
	if common.ExtractAndUnmarshalJSON([]byte(raw), &side) != nil {
		return nil
	}
	if len(side.StateReads) > stateReadMaxRequests {
		side.StateReads = side.StateReads[:stateReadMaxRequests]
	}
	return side.StateReads
}

// collectTargets lists the reads for a draft: first the floor, which does not depend on the model
// noticing anything, then the reads the model asked for. Reads past the cap are kept, unrun, so the
// trace says what was dropped.
func collectTargets(dbms *common.DatabaseManager, accountId string, plan RemediationPlan, requests []stateReadRequest) []targetSpec {
	seen := map[string]bool{}
	var out []targetSpec
	runnable := 0
	add := func(s targetSpec) {
		key := s.Command + "|" + s.KGKey + "|" + s.Target
		if seen[key] {
			return
		}
		seen[key] = true
		if s.Unresolved == "" {
			if runnable >= targetReadMaxCount {
				s = targetSpec{Target: s.Target, Unresolved: fmt.Sprintf("not read: over the limit of %d reads: %s", targetReadMaxCount, firstNonEmpty(s.Command, "knowledge graph"))}
			} else {
				runnable++
			}
		}
		out = append(out, s)
	}
	for _, a := range plan.Actions {
		argv := a.ExecuteArgv
		if len(argv) == 0 {
			argv, _ = shlex.Split(a.ExecuteCommand)
		}
		for _, s := range floorForCommand(dbms, accountId, argv) {
			add(s)
		}
	}
	for _, s := range secretAndConfigMapKeys(plan, accountId) {
		add(s)
	}
	for _, r := range requests {
		add(requestedRead(accountId, r))
	}
	return out
}

// floorForCommand: reads every write of a target type gets, whatever the model noticed -- admission
// limits & owner for a namespaced object, current policies for an IAM role (writes replace documents).
func floorForCommand(dbms *common.DatabaseManager, accountId string, argv []string) []targetSpec {
	if len(argv) < 2 {
		return nil
	}
	cmd, ok := commandFromArgv(argv)
	if !ok || isReadOnlyCommand(cmd) {
		return nil
	}
	switch argv[0] {
	case "kubectl":
		kind, name, ns := kubectlTarget(argv)
		if !kubeNSPattern.MatchString(ns) {
			return nil
		}
		var out []targetSpec
		if createsPods(argv) {
			out = append(out, targetSpec{
				Target:  "ResourceQuota and LimitRange in namespace " + ns,
				Command: "kubectl get resourcequota,limitrange -n " + ns + " -o yaml",
			})
		}
		if name != "" && isWorkloadKind(kind) && dbms != nil {
			if res := loadResource(dbms, accountId, name, "", kind, ns); res != nil {
				// Owner names come from the workload's own labels, which anyone who can annotate it
				// controls: only a valid Kubernetes name is ever placed in a command.
				app, release := workloadOwner(res.Meta)
				if kubeNamePattern.MatchString(app) {
					out = append(out, targetSpec{Target: "Argo CD application " + app + " (sync policy and source)",
						Command: "kubectl get applications.argoproj.io -A --field-selector metadata.name=" + app + " -o json"})
				}
				if kubeNamePattern.MatchString(release) {
					out = append(out, targetSpec{Target: "Helm release " + release + " values (secret-looking values redacted)",
						Command: "helm get values " + release + " -n " + ns})
				}
			}
		}
		return out
	case "aws":
		role := argValue(argv, "--role-name")
		if argv[1] != "iam" || !awsNamePattern.MatchString(role) {
			return nil
		}
		out := []targetSpec{
			{Target: "inline policies of IAM role " + role, Command: "aws iam list-role-policies --role-name " + role},
			{Target: "managed policies attached to IAM role " + role, Command: "aws iam list-attached-role-policies --role-name " + role},
		}
		if policy := argValue(argv, "--policy-name"); awsNamePattern.MatchString(policy) {
			out = append(out, targetSpec{Target: "inline policy " + policy + " of IAM role " + role,
				Command: "aws iam get-role-policy --role-name " + role + " --policy-name " + policy})
		}
		return out
	}
	return nil
}

// createsPods reports a kubectl write after which the namespace must admit new pods or larger
// requests, which is when a quota or LimitRange can refuse it. A label, annotation or delete cannot.
func createsPods(argv []string) bool {
	switch argv[1] {
	case "set", "scale", "patch", "apply", "create", "replace", "run", "autoscale":
		return true
	case "rollout":
		return len(argv) > 2 && (argv[2] == "restart" || argv[2] == "undo")
	}
	return false
}

var (
	kubeNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	kubeNSPattern   = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	awsNamePattern  = regexp.MustCompile(`^[\w+=,.@-]{1,128}$`)
)

// requestedRead: the draft is untrusted (its evidence can carry injected text) -> cloud reads are
// refused, Secret/ConfigMap reads become key-name lookups, and the rest meets readRejection in readOne.
func requestedRead(accountId string, r stateReadRequest) targetSpec {
	target := "read the draft asked for"
	if r.Concern != "" {
		target += " (" + truncate(r.Concern, 40) + ")"
	}
	cmd, ok := commandFromArgv(r.Argv)
	if !ok || len(r.Argv) < 2 {
		return targetSpec{Target: target, Unresolved: "not expressible as a command"}
	}
	switch r.Argv[0] {
	case "aws", "gcloud", "az", "gsutil":
		// A cloud read can return a secret value under any name (API keys, publishing profiles, user
		// data), so only the IAM reads NudgeBee builds itself are run.
		return targetSpec{Target: target, Unresolved: "refused: cloud reads are not taken from the plan: " + truncate(cmd, 200)}
	case "kubectl":
		if r.Argv[1] != "get" && r.Argv[1] != "describe" {
			break
		}
		kind, name, ns := kubectlTarget(r.Argv)
		ref := configObjectRef{namespace: ns, name: name}
		switch base, _, _ := strings.Cut(kind, "."); base {
		case "secret", "secrets":
			ref.kind = "Secret"
		case "cm", "configmap", "configmaps":
			ref.kind = "ConfigMap"
		default:
			return targetSpec{Target: target, Command: cmd}
		}
		if !kubeNamePattern.MatchString(name) || !kubeNSPattern.MatchString(ns) {
			return targetSpec{Target: target, Unresolved: "a Secret or ConfigMap is looked up by name and namespace, for its key names"}
		}
		return ref.spec(accountId)
	}
	return targetSpec{Target: target, Command: cmd}
}

// kubectlTarget returns the kind (lower-cased), name and namespace a kubectl command names, wherever
// the flags sit: "deployment/x" or "deployment x" after the verb (and set/rollout's sub-verb).
func kubectlTarget(argv []string) (kind, name, ns string) {
	var pos []string
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
			continue
		}
		flag, val, hasVal := strings.Cut(a, "=")
		if !hasVal && kubectlFlagTakesValue[flag] && i+1 < len(argv) {
			i++
			val = argv[i]
		}
		if flag == "-n" || flag == "--namespace" {
			ns = val
		}
	}
	if len(pos) > 0 && (pos[0] == "set" || pos[0] == "rollout") {
		pos = pos[1:]
	}
	if len(pos) < 2 {
		return "", "", ns
	}
	if k, n, ok := strings.Cut(pos[1], "/"); ok {
		return strings.ToLower(k), n, ns
	}
	if len(pos) > 2 {
		name = pos[2]
	}
	return strings.ToLower(pos[1]), name, ns
}

var (
	kubectlFlagTakesValue = map[string]bool{"-n": true, "--namespace": true, "-l": true, "--selector": true, "--field-selector": true, "-o": true, "--output": true}
	kubectlReadOutputs    = map[string]bool{"yaml": true, "json": true}
	readTokenPattern      = regexp.MustCompile(`^[A-Za-z0-9][-A-Za-z0-9._/:@+=,]*$`)
	iamFloorReads         = map[string]bool{"list-role-policies": true, "list-attached-role-policies": true, "get-role-policy": true}
)

// readRejection is the one check every read meets before an executor, whoever built it; "" means
// allowed. It is an allowlist judged on the argv the executor will run: kubectl get with namespace,
// selector and yaml|json output (the only shapes sanitizeRead can show), helm get values/list, and the
// IAM role reads the floor builds. Names taken from cluster labels or the model pass the same check.
func readRejection(command string) string {
	if why := remediationCommandRejection(command); why != "" {
		return why
	}
	argv, err := shlex.Split(command)
	if err != nil || len(argv) < 2 {
		return "not a command"
	}
	switch argv[0] {
	case "kubectl":
		if argv[1] != "get" {
			return "only kubectl get"
		}
		structured := false
		for i := 2; i < len(argv); i++ {
			a := argv[i]
			if !strings.HasPrefix(a, "-") {
				if !readTokenPattern.MatchString(a) {
					return "not a resource or name"
				}
				continue
			}
			flag, val, hasVal := strings.Cut(a, "=")
			switch flag {
			case "-A", "--all-namespaces":
				continue
			case "-n", "--namespace", "-l", "--selector", "--field-selector", "-o", "--output":
			default:
				return "flag " + flag + " is not allowed in a read"
			}
			if !hasVal {
				if i+1 >= len(argv) {
					return "flag " + flag + " has no value"
				}
				i++
				val = argv[i]
			}
			if flag == "-o" || flag == "--output" {
				if !kubectlReadOutputs[val] {
					return "output must be yaml or json"
				}
				structured = true
			}
		}
		if !structured {
			return "output must be -o yaml or -o json"
		}
		if tools.KubectlBlockedKind(command) != "" {
			return "a credential-bearing kind"
		}
		return ""
	case "helm":
		sub := argv[1]
		if len(argv) > 2 && sub == "get" {
			sub += " " + argv[2]
		}
		// Not helm status or get manifest/all: they return the rendered manifests, Secrets included.
		if sub != "get values" && sub != "list" {
			return "only helm get values and list"
		}
		for i := 2; i < len(argv); i++ {
			a := argv[i]
			if !strings.HasPrefix(a, "-") {
				if !readTokenPattern.MatchString(a) {
					return "not a release name"
				}
				continue
			}
			flag, _, hasVal := strings.Cut(a, "=")
			switch flag {
			case "-a", "--all", "-A", "--all-namespaces":
				continue
			case "-n", "--namespace", "-o", "--output":
				if !hasVal {
					i++
				}
				continue
			}
			return "flag " + flag + " is not allowed in a read"
		}
		return ""
	case "aws":
		if len(argv) < 5 || argv[1] != "iam" || !iamFloorReads[argv[2]] {
			return "only the IAM role reads NudgeBee builds"
		}
		for i := 3; i < len(argv); i += 2 {
			if (argv[i] != "--role-name" && argv[i] != "--policy-name") || i+1 >= len(argv) || !awsNamePattern.MatchString(argv[i+1]) {
				return "flag " + argv[i] + " is not allowed in a read"
			}
		}
		return ""
	}
	return "only kubectl and helm reads"
}

// secretAndConfigMapKeys looks up the key names of every Secret and ConfigMap the draft names, so a
// plan never asks for a value that already exists under another key.
func secretAndConfigMapKeys(plan RemediationPlan, accountId string) []targetSpec {
	text := plan.RootCause + " " + plan.Summary
	for _, a := range plan.Actions {
		text += " " + a.ExecuteCommand + " " + a.Title
		if a.RequiredChange != nil {
			text += " " + a.RequiredChange.Target + " " + a.RequiredChange.Change
		}
	}
	var out []targetSpec
	for _, ref := range namedConfigObjects(text) {
		out = append(out, ref.spec(accountId))
	}
	return out
}

type configObjectRef struct{ kind, namespace, name string }

// spec is the knowledge-graph lookup of the object's key names.
func (r configObjectRef) spec(accountId string) targetSpec {
	kgKind := "ConfigMap"
	if r.kind == "Secret" {
		kgKind = "K8sSecret"
	}
	return targetSpec{
		Target: r.kind + " " + r.namespace + "/" + r.name + " (key names only)",
		KGKey:  fmt.Sprintf("k8s:%s::%s:%s:%s", accountId, kgKind, r.namespace, r.name),
	}
}

// namedConfigObjects finds "Secret ns/name" / "ConfigMap ns/name" and kubectl "secret NAME -n NS" /
// "configmap NAME -n NS" references.
func namedConfigObjects(text string) []configObjectRef {
	var out []configObjectRef
	words := strings.Fields(text)
	for i := 0; i+1 < len(words); i++ {
		kind := ""
		switch strings.ToLower(strings.Trim(words[i], "`'\"(:,")) {
		case "secret", "secret/":
			kind = "Secret"
		case "configmap", "configmap/":
			kind = "ConfigMap"
		}
		if kind == "" {
			continue
		}
		next := strings.Trim(words[i+1], "`'\".,;:()")
		if ns, name, ok := strings.Cut(next, "/"); ok && ns != "" && name != "" {
			out = append(out, configObjectRef{kind, ns, name})
			continue
		}
		if i+2 < len(words) {
			if _, ns, ok := strings.Cut(words[i+2], "="); ok && (strings.HasPrefix(words[i+2], "-n=") || strings.HasPrefix(words[i+2], "--namespace=")) {
				out = append(out, configObjectRef{kind, strings.Trim(ns, "`'\".,;:()"), next})
				continue
			}
		}
		if i+3 < len(words) && (words[i+2] == "-n" || words[i+2] == "--namespace") {
			out = append(out, configObjectRef{kind, strings.Trim(words[i+3], "`'\".,;:()"), next})
		}
	}
	return out
}

// readTargets performs the reads concurrently through the account's executor, within a total budget.
// A read that fails, times out or is refused is reported as unknown, never guessed. A read still
// running when the budget ends is cancelled through its context and writes no audit row once the plan is returned.
func readTargets(ctx *security.RequestContext, accountId string, specs []targetSpec) []targetRead {
	budget, cancel := context.WithTimeout(ctx.GetContext(), targetReadBudget)
	defer cancel()
	readCtx := ctx.WithContext(budget)
	results := make([]targetRead, len(specs))
	for i, s := range specs {
		results[i] = targetRead{Target: s.Target, Source: firstNonEmpty(s.Command, "knowledge graph"), Unknown: "not read within the time budget"}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := readOne(readCtx, accountId, s)
			mu.Lock()
			results[i] = r
			mu.Unlock()
		}()
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-budget.Done():
	}
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(results)
}

func readOne(ctx *security.RequestContext, accountId string, s targetSpec) targetRead {
	r := targetRead{Target: s.Target, Source: firstNonEmpty(s.Command, "knowledge graph")}
	if s.Unresolved != "" {
		r.Source, r.Unknown = "not read", s.Unresolved
		return r
	}
	if s.KGKey != "" {
		keys, err := knowledgeGraphKeyNames(s.KGKey)
		switch {
		case err != nil:
			r.Unknown = "knowledge graph lookup failed"
		case keys == nil:
			r.Unknown = "not in the knowledge graph"
		case len(keys) == 0:
			// Exists with no keys: nothing to build a command from, so nothing to repair against.
		default:
			r.Content = "keys: " + strings.Join(keys, ", ")
		}
		return r
	}
	if why := readRejection(s.Command); why != "" {
		r.Source = "not run: " + s.Command
		r.Unknown = "refused: " + why
		return r
	}
	if ctx.GetContext().Err() != nil {
		r.Unknown = "not read within the time budget"
		return r
	}
	out, err := runReadOnly(ctx, accountId, s.Command)
	if ctx.GetContext().Err() != nil {
		r.Unknown = "not read within the time budget" // the plan has been returned; nothing is recorded
		return r
	}
	writeRemediationAudit(ctx, accountId, s.Command, "remediation_state_read", err == nil, audit.EventActionRead, "remediation-generate")
	if err != nil {
		r.Unknown = truncate(err.Error(), 200)
		return r
	}
	content, withheld := sanitizeRead(out)
	r.Content, r.Unknown = truncate(content, targetReadMaxBytes), withheld
	return r
}

// runReadOnly runs a read-only command through the same executor Run uses for this account.
func runReadOnly(ctx *security.RequestContext, accountId, command string) (string, error) {
	substrate := tools.RemediationSubstrateFor(tools.GetCloudProviderForAccount(accountId), command)
	if substrate.Reject != "" {
		return "", fmt.Errorf("%s", substrate.Reject)
	}
	if substrate.CloudCliTool != "" {
		return tools.ExecuteCloudCli(ctx, substrate.CloudCliTool, accountId, tools.DefaultCloudCliConversationId(accountId), command)
	}
	nbTool, found := toolcore.GetNBTool(accountId, substrate.RelayTool)
	if !found {
		return "", fmt.Errorf("no %s tool configured for this account", substrate.RelayTool)
	}
	userId := ""
	if sc := ctx.GetSecurityContext(); sc != nil {
		userId = sc.GetUserId()
	}
	toolCtx := toolcore.NewNbToolContext(ctx, nbTool, accountId, userId, "", "", "", command, nil, "", toolcore.NBQueryConfig{}, "")
	result, err := tools.ExecuteContainerJob(toolCtx, substrate.RelayJob, command, accountId, map[string]any{}, true)
	var exitErr *tools.RelayCommandExitError
	if errors.As(err, &exitErr) {
		// The command ran and failed; its own words (a Forbidden, a missing CRD) say why.
		if _, stderr, _, parsed := parseRelayExecResult(fmt.Sprint(result)); parsed && strings.TrimSpace(stderr) != "" {
			return "", fmt.Errorf("%s", strings.TrimSpace(stderr))
		}
	}
	if err != nil {
		return "", err
	}
	raw := fmt.Sprint(result)
	if stdout, stderr, exitCode, parsed := parseRelayExecResult(raw); parsed {
		if exitCode != 0 {
			return "", fmt.Errorf("%s", firstNonEmpty(stderr, fmt.Sprintf("exit code %d", exitCode)))
		}
		return stdout, nil
	}
	return raw, nil
}

// knowledgeGraphKeyNames returns the data key names the knowledge graph holds for one Secret or
// ConfigMap node, nil when there is no such node.
func knowledgeGraphKeyNames(uniqueKey string) ([]string, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err := dbms.Db.QueryRowx(`SELECT COALESCE(properties->'data_keys', '[]'::jsonb) FROM knowledge_graph_node WHERE unique_key = $1 LIMIT 1`, uniqueKey).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	keys := []string{}
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}

// renderTargetState writes the reads as the facts the repair pass builds commands from.
func renderTargetState(reads []targetRead, at time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Target state (read live at %s)\n", at.UTC().Format(time.RFC3339))
	for _, r := range reads {
		switch {
		case r.Content != "":
			fmt.Fprintf(&b, "### %s\nRead with: %s\n%s\n", r.Target, r.Source, r.Content)
		case r.Unknown != "":
			fmt.Fprintf(&b, "### %s\nUnknown: %s\n", r.Target, r.Unknown)
		default:
			fmt.Fprintf(&b, "### %s\nNone exists.\n", r.Target)
		}
	}
	return strings.TrimSpace(b.String())
}

func argValue(argv []string, name string) string {
	for i, a := range argv {
		if a == name && i+1 < len(argv) {
			return argv[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
	}
	return ""
}

func isWorkloadKind(kind string) bool {
	k, _, _ := strings.Cut(strings.ToLower(kind), ".")
	switch strings.TrimSuffix(k, "s") {
	case "deployment", "deploy", "statefulset", "st", "daemonset", "d":
		return true
	}
	return false
}

// workloadOwner returns the Argo CD application and Helm release that own a workload, from its labels.
func workloadOwner(meta map[string]any) (argoApp, helmRelease string) {
	config, _ := meta["config"].(map[string]any)
	labels, _ := config["labels"].(map[string]any)
	annotations, _ := config["annotations"].(map[string]any)
	argoApp = scalarString(labels["argocd.argoproj.io/instance"])
	if argoApp == "" {
		if id := scalarString(annotations["argocd.argoproj.io/tracking-id"]); id != "" {
			argoApp, _, _ = strings.Cut(id, ":")
		}
	}
	helmRelease = scalarString(annotations["meta.helm.sh/release-name"])
	return argoApp, helmRelease
}

// remediationTargetReadsKey turns the read-and-repair pass off (it is on by default).
const remediationTargetReadsKey = "LLM_SERVER_REMEDIATION_TARGET_READS_ENABLED"

// repairAgainstTargetState reads what the draft changes and, when that state bears on it, asks the
// model once more with the draft and the state. One repair, no loop; a failed repair keeps the draft.
func repairAgainstTargetState(ctx *security.RequestContext, request RemediationGenerateRequest, llm func(system, human string) (string, error),
	systemPrompt string, draft RemediationPlan, trace *RemediationTrace, artifacts []string) RemediationPlan {
	if len(draft.Actions) == 0 || !config.Config.GetBool(remediationTargetReadsKey, true) {
		return draft
	}
	dbms, _ := common.GetDatabaseManager(common.Metastore)
	trace.StateReadRequests = draftStateReads(trace.RawOutput)
	specs := collectTargets(dbms, request.AccountId, draft, trace.StateReadRequests)
	if len(specs) == 0 {
		return draft
	}
	start := time.Now()
	reads := targetReader(ctx, request.AccountId, specs)
	trace.TargetReads = reads
	trace.TargetReadMillis = time.Since(start).Milliseconds()
	if !slices.ContainsFunc(reads, func(r targetRead) bool { return r.Content != "" }) {
		return draft
	}
	draftJSON, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return draft
	}
	human := trace.HumanMessage + "\n\n## Draft plan\n" + string(draftJSON) + "\n\n" + renderTargetState(reads, remediationNow())
	raw, err := llm(systemPrompt, human)
	trace.RepairRawOutput = raw
	if err != nil {
		ctx.GetLogger().Warn("remediation_generate: repair pass failed, keeping the draft", "error", err)
		return draft
	}
	var repaired RemediationPlan
	if err := common.ExtractAndUnmarshalJSON([]byte(raw), &repaired); err != nil || (len(repaired.Actions) == 0 && !completePlanWithNoActions(raw, repaired)) {
		ctx.GetLogger().Warn("remediation_generate: repair pass returned no usable plan, keeping the draft", "error", err)
		return draft
	}
	trace.DraftPlan = &draft
	trace.RepairValidation = normalizePlan(&repaired, artifacts, request.AlertRule)
	return repaired
}

// completePlanWithNoActions: a full plan with explicit "actions": [] means the live state already has
// the change -> accept; partial/malformed output keeps the draft.
func completePlanWithNoActions(raw string, p RemediationPlan) bool {
	var keys map[string]json.RawMessage
	if common.ExtractAndUnmarshalJSON([]byte(raw), &keys) != nil {
		return false
	}
	actions, ok := keys["actions"]
	return ok && strings.TrimSpace(string(actions)) == "[]" && strings.TrimSpace(p.RootCause) != "" && strings.TrimSpace(p.Summary) != "" && len(p.Hypotheses) > 0
}

// secretLookingKey matches value names that commonly hold credentials. A field named just "key" is a
// name (a tag's Key, a metadata item's key), not a secret.
func secretLookingKey(name string) bool {
	n := strings.ToLower(name)
	for _, m := range []string{"pass", "pwd", "secret", "token", "credential", "apikey", "api_key", "api-key", "privatekey", "private_key", "auth", "cert", "dsn", "connectionstring", "connection_string", "cookie"} {
		if strings.Contains(n, m) {
			return true
		}
	}
	return n != "key" && strings.HasSuffix(n, "key")
}

// secretReferenceKeys: maps whose name/key name a Secret & key, never a value -- kept so a fix can
// repoint a secretKeyRef; anything else under them is still redacted.
var secretReferenceKeys = map[string]bool{"secretKeyRef": true, "configMapKeyRef": true, "secretRef": true, "configMapRef": true}

var (
	cloudAccountLine = regexp.MustCompile(`^\[account: [^\]\n]*\]\s*\n`)
	// urlUserinfo is the user[:password] part of scheme://user:password@host, anywhere in a string.
	urlUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/?#@"']+@`)
	// assignment is NAME=value, as in a startup script, an env file, a flag or a query string.
	assignment = regexp.MustCompile(`([A-Za-z_][\w.-]*)=("[^"]*"|'[^']*'|[^\s"'&;,]+)`)
)

// sanitizeRead reduces a read's output to what a plan needs before it reaches the model or the trace,
// "" when it found nothing (empty output, "No resources found", an empty List), or says why it was withheld. A structured document
// (after the cloud CLI's "[account: x]" line) is parsed once: copies of the spec (managedFields, the
// last-applied annotation, Argo CD's inline Helm values) are dropped, data/stringData blanked, and
// secret values redacted by name and shape. A kubectl table is kept line for line; anything else
// cannot be redacted reliably and is not shown. The tenant-independent tool-output rules run last.
func sanitizeRead(out string) (content, withheld string) {
	out, prefix := strings.TrimSpace(out), ""
	if m := cloudAccountLine.FindString(out); m != "" {
		prefix, out = m, strings.TrimSpace(out[len(m):])
	}
	if out == "" || strings.HasPrefix(out, "No resources found") {
		return "", ""
	}
	var root yaml.Node
	if yaml.Unmarshal([]byte(out), &root) == nil && len(root.Content) == 1 &&
		(root.Content[0].Kind == yaml.MappingNode || root.Content[0].Kind == yaml.SequenceNode) {
		doc := root.Content[0]
		kind, items := "", -1
		for i := 0; doc.Kind == yaml.MappingNode && i+1 < len(doc.Content); i += 2 {
			switch doc.Content[i].Value {
			case "kind":
				kind = doc.Content[i+1].Value
			case "items":
				items = len(doc.Content[i+1].Content)
			}
		}
		if len(doc.Content) == 0 || (kind == "List" && items == 0) {
			return "", ""
		}
		redactNode(&root, false)
		b, err := yaml.Marshal(&root)
		if err != nil {
			return "", "output not shown: could not be re-encoded after redaction"
		}
		return egressfilter.RedactSecrets(prefix + string(b)), ""
	}
	lines := strings.Split(out, "\n")
	if f := strings.Fields(lines[0]); len(f) > 0 && (f[0] == "NAME" || f[0] == "NAMESPACE") && strings.ToUpper(lines[0]) == lines[0] {
		for i := range lines {
			lines[i] = redactString(lines[i])
		}
		return egressfilter.RedactSecrets(prefix + strings.Join(lines, "\n")), ""
	}
	return "", "output not shown: not a structured document or a kubectl table, so it cannot be redacted reliably"
}

// redactNode walks a parsed document: drops copies of the spec, blanks data maps and Helm manifests,
// and redacts every value under a secret-looking key, every env-style {name|key: SECRET_NAME, value: ...}
// value, a secret flag's value in args, and credentials inside any string. Containers are re-emitted in
// block style so JSON input reads as YAML.
func redactNode(n *yaml.Node, secret bool) {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		n.Style = 0
		for _, c := range n.Content {
			redactNode(c, secret)
		}
	case yaml.MappingNode:
		n.Style = 0
		entrySecret := false
		for i := 0; i+1 < len(n.Content); i += 2 {
			switch n.Content[i].Value {
			case "name", "Name", "key", "Key":
				entrySecret = entrySecret || (n.Content[i+1].Kind == yaml.ScalarNode && secretLookingKey(n.Content[i+1].Value))
			}
		}
		kept := n.Content[:0]
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i].Value, n.Content[i+1]
			switch {
			case k == "managedFields" || k == "kubectl.kubernetes.io/last-applied-configuration" || k == "valuesObject" || (k == "values" && v.Kind == yaml.ScalarNode):
				continue
			case (k == "data" || k == "stringData" || k == "binaryData") && v.Kind == yaml.MappingNode, k == "manifest":
				// A Helm manifest is rendered YAML, Secrets included, in one string.
				v = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "[REDACTED]"}
			case secretReferenceKeys[k] && v.Kind == yaml.MappingNode:
				for j := 0; j+1 < len(v.Content); j += 2 {
					if ck := v.Content[j].Value; (ck == "name" || ck == "key" || ck == "optional") && v.Content[j+1].Kind == yaml.ScalarNode {
						continue // names a Secret and its key; holds no value
					}
					redactNode(v.Content[j+1], true)
				}
			case strings.EqualFold(k, "value") && v.Kind == yaml.ScalarNode && entrySecret:
				redactScalar(v)
			case (k == "args" || k == "command") && v.Kind == yaml.SequenceNode:
				// "--password", "hunter2" as two arguments -> the next one is the value.
				for idx, item := range v.Content {
					if flag := item.Value; item.Kind == yaml.ScalarNode && strings.HasPrefix(flag, "-") && !strings.Contains(flag, "=") &&
						secretLookingKey(strings.TrimLeft(flag, "-")) && idx+1 < len(v.Content) {
						if next := v.Content[idx+1]; next.Kind == yaml.ScalarNode && !strings.HasPrefix(next.Value, "-") {
							redactScalar(next)
						}
					}
				}
				redactNode(v, secret)
			default:
				redactNode(v, secret || secretLookingKey(k))
			}
			kept = append(kept, n.Content[i], v)
		}
		n.Content = kept
	case yaml.ScalarNode:
		if secret && n.Value != "" {
			redactScalar(n)
		} else if r := redactString(n.Value); r != n.Value {
			n.Value, n.Tag, n.Style = r, "!!str", 0
		}
	}
}

func redactScalar(n *yaml.Node) { n.Value, n.Tag, n.Style = "[REDACTED]", "!!str", 0 }

// redactString blanks a URL's user[:password] part and the value of every NAME=value whose name looks
// secret, wherever they sit in a string.
func redactString(s string) string {
	s = urlUserinfo.ReplaceAllString(s, "${1}[REDACTED]@")
	return assignment.ReplaceAllStringFunc(s, func(m string) string {
		if name, _, _ := strings.Cut(m, "="); secretLookingKey(name) {
			return name + "=[REDACTED]"
		}
		return m
	})
}
