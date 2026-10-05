package api

import (
	"fmt"
	"github.com/google/shlex"
	"nudgebee/llm/tools/core"
	"regexp"
	"strings"
)

// Deterministic validation of a generated remediation plan.
//
// Why this exists as code rather than as more prompt rules: the generation prompt already forbids
// every defect handled here — it asks for a clearly-named placeholder, for a verify that "must
// observe the thing the action changed", against selectors "that return empty on success", and for
// a rollback on every destructive command. An audit of the 77 plans generated between 2026-07-26
// and 2026-09-14 found those four rules broken 3, 8, 4 and 27 times respectively. A rule the model
// already has and does not follow is not fixed by restating it; it is fixed by a check that binds.
//
// Everything below is a pure function of the plan text. No environment lookup, no second model
// call, so it adds no latency and cannot itself fail at runtime.

// placeholderMaxConfidence sits under maxMitigationConfidence: a command carrying a stand-in value
// cannot be run as-is, so it ranks last among mitigations.
const placeholderMaxConfidence = Confidence(20)

// placeholderValue matches a stand-in the model was told to emit instead of a real secret, plus the
// other shapes it reaches for when it does not know a value. The prompt's own advice ("use a clearly
// named placeholder such as DB_PASSWORD_PLACEHOLDER") collides with a panel that runs commands
// verbatim: the audit found `kubectl set env deployment/app-prod GITHUB_WEBHOOK_SECRET=
// GITHUB_WEBHOOK_SECRET_PLACEHOLDER` offered as a fix at confidence 85 against production, which
// would overwrite a live secret with the literal string and roll the deployment.
var placeholderValue = regexp.MustCompile(`(?i)(_PLACEHOLDER\b|\bPLACEHOLDER_|\bYOUR_[A-Z0-9_]+|\bCHANGE_?ME\b|\bREPLACE_?ME\b|<[A-Za-z][A-Za-z0-9 _-]*>)`)

// commandHasPlaceholder reports whether the command carries a stand-in value rather than a real one.
func commandHasPlaceholder(command string) bool {
	return placeholderValue.MatchString(command)
}

// readOnlyKubectlVerbs are the kubectl verbs that only observe. `rollout status` and `auth can-i` are
// handled separately because they are two words.
var readOnlyKubectlVerbs = map[string]bool{
	"get": true, "describe": true, "logs": true, "top": true,
	"explain": true, "version": true, "api-resources": true, "cluster-info": true,
}

// cloudReadOperation judges the operation token, never flag values (a whole-string regex read
// `aws ec2 terminate-instances --query "list "` as a read); outside aws the first known verb decides.
func cloudReadOperation(argv []string) bool {
	// Leading global flags are skipped ("aws --region r ec2 ..."); unknown ones are taken as boolean.
	rest := argv[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
		if !strings.Contains(rest[0], "=") && cloudGlobalValueFlags[argv[0]][rest[0]] && len(rest) > 1 {
			rest = rest[1:]
		}
		rest = rest[1:]
	}
	var words []string
	for _, a := range rest {
		if strings.HasPrefix(a, "-") {
			break
		}
		words = append(words, strings.ToLower(a))
	}
	isRead := func(w string) bool {
		return w == "describe" || w == "list" || w == "show" || w == "ls" || w == "stat" ||
			strings.HasPrefix(w, "describe-") || strings.HasPrefix(w, "list-") || strings.HasPrefix(w, "get-") || strings.HasPrefix(w, "show-")
	}
	if argv[0] == "aws" {
		return len(words) >= 2 && words[1] != "ls" && words[1] != "stat" && isRead(words[1])
	}
	for _, w := range words {
		if isRead(w) {
			return true
		}
		if cloudWriteVerb(w) {
			return false
		}
	}
	return false
}

var cloudGlobalValueFlags = map[string]map[string]bool{
	"aws": {"--region": true, "--profile": true, "--output": true, "--query": true, "--endpoint-url": true, "--color": true,
		"--cli-read-timeout": true, "--cli-connect-timeout": true, "--ca-bundle": true},
	"gcloud": {"--project": true, "--account": true, "--configuration": true, "--format": true, "--verbosity": true,
		"--impersonate-service-account": true, "--billing-project": true, "--zone": true, "--region": true, "--flags-file": true},
	"az": {"--subscription": true, "--output": true, "-o": true, "--query": true},
}

// cloudWriteVerb matches the verbs gcloud, az and gsutil use for changes, so a read verb used as a
// resource name after one ("instances delete list") does not make the command a read.
func cloudWriteVerb(w string) bool {
	// Bare "run" is not here: it is gcloud's Cloud Run group ("gcloud run services describe").
	for _, p := range []string{"create", "delete", "update", "set", "add", "remove", "deploy", "start", "stop", "restart", "reset",
		"patch", "replace", "apply", "import", "export", "resize", "ssh", "scp", "cp", "mv", "rm", "rsync", "attach", "detach",
		"enable", "disable", "invoke", "execute", "run-command", "send", "put", "upload", "download", "rotate", "purge", "move", "rename", "assign", "grant", "revoke", "login", "logout", "configure"} {
		if w == p || strings.HasPrefix(w, p+"-") {
			return true
		}
	}
	return false
}

// isReadOnlyCommand reports whether the command only observes state. Used two ways: an action whose
// execute command is read-only is not a remediation at all (the audit found `kubectl get pods -l
// app=relay-server` shipped as an action at confidence 50), and a verify command that is *not*
// read-only is not a verification.
func isReadOnlyCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) < 2 {
		return false
	}
	switch fields[0] {
	case "kubectl":
		two := strings.Join(fields[1:minInt(3, len(fields))], " ")
		if fields[1] == "exec" {
			return core.ExecRemoteCommandIsReadOnly(fields)
		}
		return readOnlyKubectlVerbs[fields[1]] ||
			strings.HasPrefix(two, "rollout status") || strings.HasPrefix(two, "auth can-i")
	case "aws", "az", "gcloud", "gsutil":
		if strings.Contains(command, "ssm send-command") {
			return core.SSMPayloadIsReadOnly(command)
		}
		argv, err := shlex.Split(command)
		return err == nil && len(argv) > 1 && cloudReadOperation(argv)
	case "helm":
		return fields[1] == "list" || fields[1] == "status" || fields[1] == "get" || fields[1] == "history"
	}
	return false
}

// kubectlSetTarget pulls (verb, kind, name, namespace) out of a `kubectl set …` / `kubectl scale …`
// command so a verify can be derived that reads back the field the command wrote. Returns ok=false
// for any shape it does not recognise rather than guessing.
func kubectlSetTarget(command string) (verb, kind, name, namespace string, ok bool) {
	f := strings.Fields(command)
	if len(f) < 4 || f[0] != "kubectl" {
		return "", "", "", "", false
	}
	var rest []string
	switch {
	case f[1] == "set" && len(f) >= 5:
		verb, rest = "set "+f[2], f[3:]
	case f[1] == "scale":
		verb, rest = "scale", f[2:]
	default:
		return "", "", "", "", false
	}
	if len(rest) < 1 {
		return "", "", "", "", false
	}
	// The target is either "kind name" or "kind/name".
	if k, n, found := strings.Cut(rest[0], "/"); found {
		kind, name = k, n
	} else {
		if len(rest) < 2 {
			return "", "", "", "", false
		}
		kind, name = rest[0], rest[1]
	}
	for i := 0; i < len(f)-1; i++ {
		if f[i] == "-n" || f[i] == "--namespace" {
			namespace = f[i+1]
		}
		if strings.HasPrefix(f[i], "--namespace=") {
			namespace = strings.TrimPrefix(f[i], "--namespace=")
		}
	}
	if kind == "" || name == "" {
		return "", "", "", "", false
	}
	return verb, kind, name, namespace, true
}

// revisionedKind matches the workload kinds whose pod template is versioned, so that undoing the
// revision a `kubectl set` created restores every field it wrote at once.
var revisionedKind = map[string]bool{
	"deployment": true, "deployments": true, "deploy": true,
	"statefulset": true, "statefulsets": true, "sts": true,
	"daemonset": true, "daemonsets": true, "ds": true,
}

// settingsWritten returns the keys a `kubectl set resources|env` command writes, so a rollback can
// be checked for restoring all of them. Only these two subcommands are handled: for anything else
// the keys of a change are not recoverable from the command text, and a wrong answer here would
// discard a correct rollback.
func settingsWritten(command string) []string {
	f := strings.Fields(command)
	if len(f) < 4 || f[0] != "kubectl" || f[1] != "set" {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	switch f[2] {
	case "resources":
		// --limits=memory=2Gi,cpu=1 -> limits.memory, limits.cpu
		for _, g := range resourceFlag.FindAllStringSubmatch(command, -1) {
			for _, pair := range strings.Split(g[2], ",") {
				if k, _, ok := strings.Cut(pair, "="); ok {
					add(g[1] + "." + k)
				}
			}
		}
	case "env":
		// Positional KEY=value tokens after the target. Only flags that actually take a separate
		// value consume the next token: skipping one after every flag loses the KEY=value that
		// follows a boolean like `--overwrite`, which would silently under-report what the command
		// wrote and let an incomplete rollback pass as complete.
		for i := 4; i < len(f); i++ {
			if strings.HasPrefix(f[i], "-") {
				if flagTakesValue[f[i]] {
					i++
				}
				continue
			}
			if k, _, ok := strings.Cut(f[i], "="); ok {
				add(k)
			}
		}
	default:
		return nil
	}
	return out
}

var resourceFlag = regexp.MustCompile(`--(limits|requests)=(\S+)`)

// flagTakesValue lists the `kubectl set env` flags whose value is a separate token. The `--flag=x`
// form needs no entry: it is one token and is skipped as a flag.
var flagTakesValue = map[string]bool{
	"-c": true, "--containers": true, "-n": true, "--namespace": true,
	"-f": true, "--filename": true, "--from": true, "--keys": true, "-o": true, "--output": true,
}

// deriveRollback builds the complete inverse of a `kubectl set` that the plan's own rollback failed
// to fully reverse.
//
// `set resources` and `set env` patch the pod template, which creates a new revision, so
// `kubectl rollout undo` restores every field the execute wrote in one step — including the ones a
// hand-written rollback forgot. This is derived from the execute command, not invented: nothing
// here guesses a prior value, which is exactly why it is safe to substitute.
//
// Returns "" when the target is not a revisioned workload, so the caller clears rather than
// substituting something that would revert the wrong thing.
func deriveRollback(executeCommand string) string {
	verb, kind, name, namespace, ok := kubectlSetTarget(executeCommand)
	if !ok || (verb != "set resources" && verb != "set env" && verb != "set image") {
		return ""
	}
	if !revisionedKind[strings.ToLower(kind)] {
		return ""
	}
	cmd := fmt.Sprintf("kubectl rollout undo %s %s", kind, name)
	if namespace != "" {
		cmd += " -n " + namespace
	}
	return cmd
}

// rollbackLeavesChanged returns the settings the execute wrote that the rollback does not restore.
func rollbackLeavesChanged(exec, rollback string) []string {
	written := settingsWritten(exec)
	if len(written) == 0 {
		return nil
	}
	// `rollout undo` reverts the whole revision, so it restores everything by construction.
	if strings.Contains(rollback, "rollout undo") {
		return nil
	}
	restored := map[string]bool{}
	for _, k := range settingsWritten(rollback) {
		restored[k] = true
	}
	missing := []string{}
	for _, k := range written {
		if !restored[k] {
			missing = append(missing, k)
		}
	}
	return missing
}

// deriveVerify builds a verify command that reads back the field the execute command wrote.
//
// The audit's sharpest finding about the verify slot: 8 plans verified a `kubectl set …` with
// `kubectl rollout status`, which confirms a rollout finished and passes for whatever value was
// set — including the wrong one. The single execution in the whole dataset with a passing verify
// (sessions-api raised to 1Gi) verified exactly that way, so its pass proved nothing; what actually
// showed the fix held was the alert never firing again.
//
// Returns "" when the shape is not one we can read back, so the caller can clear a bad verify
// rather than replace it with another bad one.
func deriveVerify(executeCommand string) string {
	verb, kind, name, namespace, ok := kubectlSetTarget(executeCommand)
	if !ok {
		return deriveSsmVerify(executeCommand)
	}
	var path string
	switch verb {
	case "set resources":
		path = "{.spec.template.spec.containers[*].resources}"
	case "set env":
		path = "{.spec.template.spec.containers[*].env}"
	case "set image":
		path = "{.spec.template.spec.containers[*].image}"
	case "scale":
		path = "{.spec.replicas}"
	default:
		return ""
	}
	cmd := fmt.Sprintf("kubectl get %s %s", kind, name)
	if namespace != "" {
		cmd += " -n " + namespace
	}
	return cmd + " -o jsonpath='" + path + "'"
}

// serviceUnit matches a systemd unit name, so a derived verify can only ever carry a unit and
// never free text into the SSM shell channel.
var serviceUnit = regexp.MustCompile(`^[A-Za-z0-9@._-]+$`)

// deriveSsmVerify builds the service-state read for a host service fix delivered over SSM.
//
// Six generations on this shape produced `systemctl start postgresql`, and exactly one of them
// verified it the right way — by reading `systemctl is-active` back on the host. The other five
// read the CloudWatch alarm, which lags the fix by minutes. So the correct verify is both known
// and mechanically derivable from the execute: swap the verb.
//
// This does emit an `aws ssm send-command`, whose `--parameters` the product's metacharacter guard
// does not inspect. It adds no capability: the plan already carries a send-command to that
// instance, and this one is strictly weaker — a fixed template whose only substituted token is a
// unit name matched against `serviceUnit`, so nothing free-form reaches the channel.
//
// Returns "" unless the execute is exactly a service start on a single instance.
func deriveSsmVerify(executeCommand string) string {
	if !strings.Contains(executeCommand, "ssm send-command") {
		return ""
	}
	f := strings.Fields(core.SSMPayload(executeCommand))
	if len(f) > 0 && f[0] == "sudo" {
		f = f[1:]
	}
	if len(f) != 3 || f[0] != "systemctl" {
		return ""
	}
	switch f[1] {
	case "start", "restart", "enable":
	default:
		return ""
	}
	unit := f[2]
	if !serviceUnit.MatchString(unit) {
		return ""
	}
	instance := instanceIDFlag.FindStringSubmatch(executeCommand)
	if instance == nil {
		return ""
	}
	region := ""
	if m := regionFlag.FindStringSubmatch(executeCommand); m != nil {
		region = " --region " + m[1]
	}
	return fmt.Sprintf(
		`aws ssm send-command --instance-ids %s --document-name AWS-RunShellScript `+
			`--parameters "commands=systemctl is-active %s"%s`, instance[1], unit, region)
}

var instanceIDFlag = regexp.MustCompile(`--instance-ids\s+"?(i-[0-9a-f]+)"?`)
var regionFlag = regexp.MustCompile(`--region\s+([a-z0-9-]+)`)

// selectorFlag matches a label selector, which makes a verify unable to distinguish "the fix held"
// from "nothing matched": both print nothing and both exit 0.
var selectorFlag = regexp.MustCompile(`(^|\s)(-l|--selector)(\s|=)`)

// unobservantVerify matches read-only commands that do not read back the changed state.
// `kubectl describe` and `kubectl logs` are both read-only — so they survive the read-only gate —
// and both exit 0 regardless of whether the change actually took effect.
var unobservantVerify = regexp.MustCompile(`^kubectl\s+(describe|logs)\b`)

// alarmStateVerify matches a verify that reads the alarm instead of the thing the command changed.
//
// An alarm is a delayed, derived signal: this lab's db-down alarm needs two 60s evaluation periods
// plus ingestion, measured at 4m18s from fault to ALARM. Run straight after a fix that worked it
// still reports ALARM, so it reads as "the remediation failed" — and once it does clear, it cleared
// for whatever reason the metric moved, not because this command ran. Wrong in both directions.
var alarmStateVerify = regexp.MustCompile(`(?i)cloudwatch\s+describe-alarms|MetricAlarms\[`)

// destructiveRollback matches a rollback that removes or halts something. A rollback for a
// non-destructive action must not be destructive: the audit found "start PostgreSQL" whose offered
// rollback was `systemctl stop postgresql` via SSM — a one-click database outage sitting behind an
// Undo button.
var destructiveRollback = regexp.MustCompile(`(?i)\b(delete|destroy|terminate|purge|remove|rb|stop)\b|--replicas[= ]0\b`)

// planValidationNote records one deterministic correction, for logging and for the replay harness.
type planValidationNote struct {
	Action string
	Rule   string
	Detail string
}

// validatePlanCommands applies the deterministic command contract to every action, mutating the plan
// in place and returning what it changed. Called from normalizePlan, after kind and confidence are
// canonical and before ranking, so a demotion here also moves the action down the list.
//
// It never invents a value it cannot derive from the plan text: where a command cannot be made
// correct it is cleared or the action is dropped, rather than replaced with a guess.
func validatePlanCommands(plan *RemediationPlan) []planValidationNote {
	notes := []planValidationNote{}
	note := func(action, rule, detail string) {
		notes = append(notes, planValidationNote{Action: action, Rule: rule, Detail: detail})
	}
	// Names created by an action we had to block, so later actions that consume them can be demoted
	// too. Actions arrive in the model's own order, which is the order the operator would run them.
	blockedNames := []string{}

	kept := plan.Actions[:0]
	for i := range plan.Actions {
		a := &plan.Actions[i]
		exec := strings.TrimSpace(a.ExecuteCommand)

		// An action whose command only observes state is not a remediation. Dropping it is right:
		// the operator's list is of things that change something, and a read-only entry there is a
		// button that reports success without having done anything.
		if exec != "" && isReadOnlyCommand(exec) {
			note(a.Action, "read_only_action_dropped", exec)
			continue
		}

		// A command carrying a stand-in value must never be presented as the durable answer. It is
		// kept — the operator still needs to know what to do — but it cannot be the ranked-first
		// `fix`, and processRemediationExecute refuses to run it.
		blockedValue := exec != "" && commandHasPlaceholder(exec)
		if blockedValue {
			if a.Kind == RemediationKindFix {
				note(a.Action, "placeholder_demoted_from_fix", exec)
			}
			a.Kind = RemediationKindMitigation
			if a.Confidence > placeholderMaxConfidence {
				a.Confidence = placeholderMaxConfidence
			}
			// Verify and rollback are premised on the execute having applied a real value, and this
			// command will be refused before it applies anything.
			a.VerifyCommand = ""
			a.RollbackCommand = ""
			blockedNames = append(blockedNames, resourceNamesIn(exec)...)
		}

		// A command the Run button would refuse is not an action the operator can take. A placeholder
		// action is exempt: it is kept on purpose, to tell the operator what to do by hand.
		if !blockedValue && exec != "" && remediationCommandRejection(exec) != "" {
			note(a.Action, "execute_rejected_by_executor_dropped", exec)
			blockedNames = append(blockedNames, resourceNamesIn(exec)...)
			continue
		}

		// An action that depends on a step that cannot run inherits its uselessness. The audit found
		// a plan whose first action created a registry secret from placeholder credentials, whose
		// second attached that secret to a ServiceAccount, and whose third restarted the workload:
		// the second and third are individually well-formed and collectively make the outage worse.
		if !blockedValue && exec != "" && referencesAny(exec, blockedNames) {
			if a.Kind == RemediationKindFix {
				note(a.Action, "depends_on_blocked_action_demoted", exec)
			}
			a.Kind = RemediationKindMitigation
			if a.Confidence > placeholderMaxConfidence {
				a.Confidence = placeholderMaxConfidence
			}
		}

		if !blockedValue {
			a.VerifyCommand = validatedVerify(a, exec, note)
			a.RollbackCommand = validatedRollback(a, exec, note)
		}
		kept = append(kept, *a)
	}
	plan.Actions = kept
	return notes
}

// validatedVerify returns a verify command that actually observes what the execute command changed,
// deriving one where the execute shape allows it and clearing it otherwise. A verify that cannot
// fail is worse than none: it is read as proof.
func validatedVerify(a *RemediationAction, exec string, note func(string, string, string)) string {
	v := strings.TrimSpace(a.VerifyCommand)
	if exec == "" {
		return ""
	}
	derived := deriveVerify(exec)
	switch {
	case v == "":
		if derived != "" {
			note(a.Action, "verify_added", derived)
			return derived
		}
		return ""
	case remediationCommandRejection(v) != "":
		note(a.Action, "verify_rejected_by_executor_replaced", v)
		return derived
	case v == exec:
		note(a.Action, "verify_same_as_execute_cleared", v)
		return derived // "" when nothing can be derived
	case !isReadOnlyCommand(v):
		note(a.Action, "verify_not_read_only_replaced", v)
		return derived
	case selectorFlag.MatchString(v):
		// Prints nothing whether the fix held or the selector was wrong.
		note(a.Action, "verify_selector_replaced", v)
		return derived
	case strings.Contains(v, "rollout status") && derived != "":
		// Confirms the rollout finished, not the value that was rolled out.
		note(a.Action, "verify_rollout_status_replaced_with_value_read", v+" -> "+derived)
		return derived
	case unobservantVerify.MatchString(v) || alarmStateVerify.MatchString(v):
		// Same defect as `rollout status`, different command. `describe` prints a page the operator
		// must eyeball and exits 0 whatever the field says; `logs` reports what the workload
		// printed, not what the change set. Both pass for the wrong value, so both are read as
		// proof of a fix that may not have landed. Replaced by a value read where the execute shape
		// allows one, cleared otherwise — a verify that cannot fail is worse than none.
		if derived != "" {
			note(a.Action, "verify_does_not_observe_state_replaced", v+" -> "+derived)
		} else {
			note(a.Action, "verify_does_not_observe_state_cleared", v)
		}
		return derived
	}
	return v
}

// validatedRollback clears a rollback that would not revert the change, or would do damage.
// It never invents one: the prior value of a field the plan overwrote is not in the plan text, so
// an action left without a rollback is reported as such rather than given a fabricated undo.
func validatedRollback(a *RemediationAction, exec string, note func(string, string, string)) string {
	rb := strings.TrimSpace(a.RollbackCommand)
	if rb == "" || exec == "" {
		return rb
	}
	if remediationCommandRejection(rb) != "" {
		note(a.Action, "rollback_rejected_by_executor_cleared", rb)
		return ""
	}
	if rb == exec {
		// `rollout undo` undone by `rollout undo` puts the broken revision back.
		note(a.Action, "rollback_same_as_execute_cleared", rb)
		return ""
	}
	// A restart changes no desired state, so there is nothing to revert — and `rollout undo`, the
	// rollback the model reaches for 16 times, reverts the previous real deployment instead.
	if strings.HasPrefix(exec, "kubectl rollout restart") {
		note(a.Action, "rollback_for_restart_cleared", rb)
		return ""
	}
	if destructiveRollback.MatchString(rb) && !destructiveRollback.MatchString(exec) {
		note(a.Action, "destructive_rollback_cleared", rb)
		return ""
	}
	// A rollback that restores only some of what the execute wrote is worse than an honest absence:
	// it is offered as "Undo", the operator clicks it, and the workload is left in a third state
	// that is neither the original nor the fix. The audit found four — three `set resources` that
	// restored limits and left requests raised, and one `set env` that restored one of two
	// ZooKeeper variables.
	if missing := rollbackLeavesChanged(exec, rb); len(missing) > 0 {
		if derived := deriveRollback(exec); derived != "" {
			note(a.Action, "rollback_incomplete_replaced_with_revision_undo",
				rb+" (leaves "+strings.Join(missing, ",")+" changed) -> "+derived)
			return derived
		}
		note(a.Action, "rollback_incomplete_cleared", rb+" leaves "+strings.Join(missing, ",")+" changed")
		return ""
	}
	return rb
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// resourceNamesIn returns the name of the object a command CREATES, so a later action that consumes
// it can be recognised as depending on a step that could not run. Only `kubectl create` is matched:
// a broader sweep picks up the namespace and then demotes every other action in it, which is a
// different (and wrong) rule.
func resourceNamesIn(command string) []string {
	f := strings.Fields(command)
	if len(f) < 4 || f[0] != "kubectl" || f[1] != "create" {
		return nil
	}
	// `kubectl create secret docker-registry NAME …` carries an extra sub-type token.
	idx := 3
	if f[2] == "secret" && len(f) > 4 {
		idx = 4
	}
	if idx >= len(f) || strings.HasPrefix(f[idx], "-") {
		return nil
	}
	return []string{f[idx]}
}

// referencesAny reports whether the command mentions any of the given names.
func referencesAny(command string, names []string) bool {
	for _, n := range names {
		if n != "" && strings.Contains(command, n) {
			return true
		}
	}
	return false
}

// The read-only-remote-command allowlist (readOnlyRemoteCommands equivalent), the SSM payload
// parser, and execRemoteCommandIsReadOnly/ssmPayloadIsReadOnly moved to tools/core
// (readonly_remote.go) so the live investigation gate (tools/tool_kubectl.go,
// tools/tool_cloud_aws.go) can share the same read-only-command notion instead of maintaining an
// independent, narrower copy. See core.ReadOnlyRemoteCommands's doc comment for why entries there
// must be safe under "runs with no human confirmation," a stricter bar than this file's own
// confidence-scoring use.
