package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitKubectlStderrNoise(t *testing.T) {
	cases := []struct {
		name       string
		response   string
		wantStdout string
		wantStderr string
	}{
		{"empty input", "", "", ""},
		{"no noise unchanged", "pod-1   Running\npod-2   Running", "pod-1   Running\npod-2   Running", ""},
		{"defaulted-container notice split", "Defaulted container \"app\" out of: app, sidecar\nhello world", "hello world", "Defaulted container \"app\" out of: app, sidecar"},
		{"multiple noise prefixes split together", "Warning: A is deprecated\nW0406 12:00:00 klog warning\nreal output", "real output", "Warning: A is deprecated\nW0406 12:00:00 klog warning"},
		{"all noise — stdout empty", "Warning: A is deprecated\nWarning: B is deprecated", "", "Warning: A is deprecated\nWarning: B is deprecated"},
		{"trailing notices stay with stdout", "real output\nWarning: trailing notice stays with stdout", "real output\nWarning: trailing notice stays with stdout", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := splitKubectlStderrNoise(tc.response)
			assert.Equal(t, tc.wantStdout, stdout, "stdout mismatch")
			assert.Equal(t, tc.wantStderr, stderr, "stderr mismatch")
		})
	}
}

func TestKubectlResourceKind(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"get pods plural", "kubectl get pods -A", "pods"},
		{"get pod singular", "kubectl get pod foo -n bar", "pods"},
		{"get po short", "kubectl get po", "pods"},
		{"describe pod slashform", "kubectl describe pod/foo -n bar", "pods"},
		{"logs verb implies pods", "kubectl logs my-pod -n bar --tail 200", "pods"},
		{"exec verb implies pods", "kubectl exec my-pod -- ls /", "pods"},
		{"port-forward verb implies pods", "kubectl port-forward pod/foo 8080:8080", "pods"},

		{"get services", "kubectl get services -n bar", "services"},
		{"get svc short", "kubectl get svc", "services"},
		{"describe service slashform", "kubectl describe service/web -n bar", "services"},

		{"get namespace", "kubectl get namespace", "namespaces"},
		{"get ns short", "kubectl get ns", "namespaces"},
		{"get namespaces plural", "kubectl get namespaces", "namespaces"},

		{"get pvc", "kubectl get pvc -n bar", "pvc"},
		{"get persistentvolumeclaim", "kubectl get persistentvolumeclaim -n bar", "pvc"},
		{"describe pvc slashform", "kubectl describe pvc/data-postgres-0 -n bar", "pvc"},

		{"get pv", "kubectl get pv", "pv"},
		{"get persistentvolume", "kubectl get persistentvolume", "pv"},

		{"get nodes", "kubectl get nodes", "nodes"},
		{"get node singular", "kubectl get node ip-10-0-0-1", "nodes"},
		{"get no short", "kubectl get no", "nodes"},
		{"top nodes", "kubectl top nodes", "nodes"},

		{"comma list takes first kind", "kubectl get po,svc -A", "pods"},

		{"workload kinds fall through", "kubectl get deployments -n bar", ""},
		{"statefulsets fall through", "kubectl get statefulsets -n bar", ""},
		{"daemonsets fall through", "kubectl get daemonsets", ""},
		{"events fall through", "kubectl get events --sort-by=.metadata.creationTimestamp", ""},
		{"version subcommand", "kubectl version", ""},
		{"cluster-info", "kubectl cluster-info", ""},
		{"empty", "", ""},

		{"missing kubectl prefix get pvc", "get pvc -A", "pvc"},
		{"flag noise before verb", "kubectl --kubeconfig /tmp/kc get pvc -A", "pvc"},
		{"flag between verb and kind", "kubectl get -o yaml pvc -n bar", "pvc"},
		{"context flag stripped", "kubectl --context prod get nodes", "nodes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, kubectlResourceKind(tc.command))
		})
	}
}

func TestKubectlNamespace(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"-n space form", "kubectl get pvc -n prod", "prod"},
		{"-n=joined form", "kubectl get pvc -n=prod", "prod"},
		{"--namespace space form", "kubectl get pods --namespace staging", "staging"},
		{"--namespace=joined form", "kubectl describe svc/web --namespace=staging", "staging"},
		{"quoted namespace", `kubectl get pvc -n "prod"`, "prod"},
		{"all-namespaces short skips", "kubectl get pvc -A", ""},
		{"all-namespaces long skips", "kubectl get pvc --all-namespaces", ""},
		{"no namespace given", "kubectl get nodes", ""},
		{"-n with no following value", "kubectl get pvc -n", ""},
		{"-n followed by another flag", "kubectl get pvc -n -o yaml", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, kubectlNamespace(tc.command))
		})
	}
}

func TestKubectlUIReference(t *testing.T) {
	cases := []struct {
		name    string
		command string
		modules []string
		label   string
	}{
		{"pvc → pvc tab", "kubectl get pvc -A", []string{"kubernetes", "pvc"}, "View PVCs"},
		{"pod → pods tab", "kubectl get pods -A", []string{"kubernetes", "pods"}, "View Pods"},
		{"service → services tab", "kubectl get svc -n bar", []string{"kubernetes", "services"}, "View Services"},
		{"namespace → namespaces tab", "kubectl get ns", []string{"kubernetes", "namespaces"}, "View Namespaces"},
		{"pv → pv tab", "kubectl get pv", []string{"kubernetes", "pv"}, "View PVs"},
		{"node → nodes tab", "kubectl get nodes", []string{"kubernetes", "nodes"}, "View Nodes"},
		{"workload falls back to applications", "kubectl get deployments -n bar", []string{"kubernetes", "applications"}, "Check Apps & Pods"},
		{"unknown falls back to applications", "kubectl version", []string{"kubernetes", "applications"}, "Check Apps & Pods"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modules, label := kubectlUIReference(tc.command)
			assert.Equal(t, tc.modules, modules)
			assert.Equal(t, tc.label, label)
		})
	}
}

func TestKubectlBlockedKind(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		// Bypasses of the old substring blocklist (`strings.Contains(" secret")`)
		// that this tokenizer is built to catch.
		{"slash form", "kubectl get secret/my-tls -o yaml", "secret"},
		{"slash form plural", "kubectl get secrets/my-tls", "secrets"},
		{"flag value between verb and kind", "kubectl get -o yaml secret my-tls", "secret"},
		{"FQDN core group", "kubectl get secrets.v1.core", "secrets"},
		{"FQDN bitnami sealedsecrets", "kubectl get sealedsecrets.bitnami.com/foo", "sealedsecrets"},
		{"comma list with secrets", "kubectl get pods,secrets -A", "secrets"},
		{"comma list secrets first", "kubectl get secrets,services", "secrets"},
		{"namespaced describe", "kubectl describe secret my-tls -n bar", "secret"},

		// Other verbs that mutate or expose secret contents.
		{"edit", "kubectl edit secret my-tls", "secret"},
		{"delete", "kubectl delete secret my-tls", "secret"},
		{"patch", "kubectl patch secret my-tls -p '{}'", "secret"},
		{"create generic", "kubectl create secret generic foo --from-literal=a=b", "secret"},
		{"apply -f stdin", "kubectl apply -f - <<<$(cat secret.yaml)", "secret"},
		{"scale (no-op on secrets but still blocked)", "kubectl scale secret/x --replicas=1", "secret"},

		// Adjacent secret-bearing CRDs.
		{"externalsecret singular", "kubectl get externalsecret my-es", "externalsecret"},
		{"externalsecrets plural", "kubectl get externalsecrets -A", "externalsecrets"},
		{"secretproviderclass", "kubectl describe secretproviderclass my-spc", "secretproviderclass"},
		{"secretstore", "kubectl get secretstore", "secretstore"},
		{"clustersecretstore", "kubectl get clustersecretstores", "clustersecretstores"},

		// CRD short names (standard kubectl aliases) must also be blocked.
		{"externalsecret short name 'es'", "kubectl get es", "es"},
		{"secretproviderclass short name 'spc'", "kubectl get spc my-spc", "spc"},
		{"secretproviderclass short name 'spcs'", "kubectl get spcs -A", "spcs"},
		{"secretstore short name 'ss'", "kubectl get ss", "ss"},
		{"clustersecretstore short name 'css'", "kubectl get css -A", "css"},

		// Cases that MUST NOT be blocked.
		{"unrelated kind", "kubectl get pods -A", ""},
		{"pod name contains 'secret' substring", "kubectl get pod secret-rotator -n bar", ""},
		{"deployment name contains 'secrets'", "kubectl get deployment secrets-syncer", ""},
		{"configmap (different secret-store)", "kubectl get configmap my-cm -o yaml", ""},
		{"version subcommand", "kubectl version", ""},
		{"cluster-info", "kubectl cluster-info", ""},
		{"empty", "", ""},
		{"pod-only verb (exec) ignored here", "kubectl exec mypod -- ls /var/run", ""},

		// Bypass-attempt coverage.
		{"double-quoted kind", "kubectl get \"secret\"", "secret"},
		{"double-quoted slash form", "kubectl get \"secret/my-tls\" -o yaml", "secret"},
		{"single-quoted kind", "kubectl get 'sealedsecret'", "sealedsecret"},
		{"uppercase verb + mixed-case kind", "kubectl GET Secret", "secret"},
		{"compound — get pods; get secret", "kubectl get pods && kubectl get secret", "secret"},
		{"compound — preceding non-kubectl", "cd /tmp && kubectl get secret", "secret"},
		// Gemini review bypasses (PR #31814).
		{"comma list with slash forms — secrets second", "kubectl get pods/foo,secrets/bar", "secrets"},
		{"comma list with slash forms — secret first", "kubectl get secret/a,pods/b", "secret"},
		{"comma list mixed quoted + FQDN", "kubectl get \"pods\",sealedsecrets.bitnami.com/x", "sealedsecrets"},
		{"semicolon glued to kind", "kubectl get secret;", "secret"},
		{"pipe glued to kind", "kubectl get secret|grep foo", "secret"},
		{"command substitution", "kubectl get $(echo secret)", "secret"},
		{"backtick substitution", "kubectl get `echo secret`", "secret"},
		{"redirect glued to kind", "kubectl get secret>/tmp/x", "secret"},
		{"interior double-quote bypass", "kubectl get sec\"\"ret", "secret"},
		{"interior backslash bypass", "kubectl get s\\e\\c\\r\\e\\t", "secret"},
		{"interior single-quote bypass", "kubectl get s''e''c''r''e''t", "secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, kubectlBlockedKind(tc.command))
		})
	}
}

func TestKubectlReadsSecretFilesystemPath(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"exec cat service account token", "kubectl exec mypod -- cat /var/run/secrets/kubernetes.io/serviceaccount/token", true},
		{"exec sh -c into run secrets", "kubectl exec mypod -- sh -c \"find /run/secrets -type f\"", true},
		{"cp from secret volume", "kubectl cp mypod:/var/run/secrets/foo /tmp/x", true},
		{"cp from kubelet pod volume", "kubectl cp mypod:/var/lib/kubelet/pods/abc/volumes/kubernetes.io~secret/x /tmp/y", true},
		{"attach into pod reading secrets", "kubectl attach mypod -- cat /var/run/secrets/kubernetes.io/serviceaccount/ca.crt", true},

		// Cases that MUST NOT trigger.
		{"exec without secret path", "kubectl exec mypod -- ls /tmp", false},
		{"get with secret path in name (no exec verb)", "kubectl get configmap /var/run/secrets/foo", false},
		{"path in cluster-info dump (no relevant verb)", "kubectl cluster-info dump --output-directory=/var/run/secrets", false},
		{"empty", "", false},
		{"port-forward (not exec/cp/attach)", "kubectl port-forward mypod 8080:8080", false},
		// Gemini review bypasses (PR #31814).
		{"interior double-quote bypass in path", "kubectl exec mypod -- cat /var/run/sec\"\"rets/foo", true},
		{"interior backslash bypass in path", "kubectl exec mypod -- cat /var/run/se\\crets/foo", true},
		{"relative path via serviceaccount marker", "kubectl exec mypod -- sh -c \"cd /var/run && cat secrets/kubernetes.io/serviceaccount/token\"", true},
		{"kubernetes.io serviceaccount marker on its own", "kubectl exec mypod -- cat kubernetes.io/serviceaccount/token", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, kubectlReadsSecretFilesystemPath(tc.command))
		})
	}
}

// TestKubectlErrorHint_Patterns pins the hint discriminator added for
// issue #32240. Two patterns hit, anything else falls through to "".
func TestKubectlErrorHint_Patterns(t *testing.T) {
	cases := []struct {
		name     string
		rawError string
		want     string // substring expected in hint; "" = no hint
	}{
		{
			name:     "shim 500 wraps a 400 Bad Request",
			rawError: `Error: Server returned 500: {"error":"status: 400 Bad Request","result":null}`,
			want:     "rejected this kubectl command as malformed",
		},
		{
			name:     "shim 500 wraps the legacy findings-not-found parser bail",
			rawError: `Error: Server returned 500: {"error":"findings field not found or is nil from data","result":null}`,
			want:     "response shape the parser didn't recognize",
		},
		{
			name:     "plain 500 with neither known body — no hint",
			rawError: `Error: Server returned 500: {"error":"some other thing","result":null}`,
			want:     "",
		},
		{
			name:     "successful 200 — no hint",
			rawError: "",
			want:     "",
		},
		{
			name:     "kubectl resource-not-found stderr does NOT collide with the 400 pattern",
			rawError: `Error from server (NotFound): pods "missing-pod": not found`,
			want:     "",
		},
		{
			name:     "case-insensitive match on shim wrapper",
			rawError: `error: server returned 500: {"error":"status: 400 bad request","result":null}`,
			want:     "rejected this kubectl command as malformed",
		},
		// Decoupling guard — PR #32243 Gemini review. The hint must fire
		// whether the error reaches us shim-wrapped ("Server returned
		// 500: ...") or directly from a Go parser (errors.New(...)). The
		// raw error string is what reaches kubectlErrorHint; require only
		// the underlying signal, not the wrapper.
		{
			name:     "direct findings-not-found error (no shim wrapper) — hint still fires",
			rawError: "findings field not found or is nil from data",
			want:     "response shape the parser didn't recognize",
		},
		{
			name:     "direct 400-Bad-Request error (no shim wrapper) — hint still fires",
			rawError: "kubectl post failed: status: 400 Bad Request",
			want:     "rejected this kubectl command as malformed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := kubectlErrorHint(c.rawError)
			if c.want == "" {
				assert.Empty(t, got, "expected no hint, got: %s", got)
				return
			}
			assert.Contains(t, got, c.want)
		})
	}
}

// TestWrapKubectlError_EnvelopeShape pins the JSON envelope shape on
// patterns that hit + verifies pass-through on patterns that don't.
func TestWrapKubectlError_EnvelopeShape(t *testing.T) {
	t.Run("400 wrap: envelope contains both hint and original_error", func(t *testing.T) {
		raw := `Error: Server returned 500: {"error":"status: 400 Bad Request","result":null}`
		wrapped := wrapKubectlError(raw, "kubectl describe ds kube-proxy -n kube-system")
		assert.Contains(t, wrapped, `"error_hint"`)
		assert.Contains(t, wrapped, `"original_error"`)
		assert.Contains(t, wrapped, "rejected this kubectl command as malformed")
		// The raw error must round-trip verbatim — string-quoting handled
		// by MarshalJson, so the JSON-encoded form of the raw error is
		// what's expected in the envelope body.
		var env map[string]string
		err := json.Unmarshal([]byte(wrapped), &env)
		assert.NoError(t, err)
		assert.Equal(t, raw, env["original_error"])
	})

	t.Run("no pattern match: pass-through unchanged", func(t *testing.T) {
		raw := "Error from server (NotFound): pods \"missing-pod\": not found"
		wrapped := wrapKubectlError(raw, "kubectl get pod missing-pod")
		assert.Equal(t, raw, wrapped)
	})

	t.Run("empty raw: pass-through empty", func(t *testing.T) {
		wrapped := wrapKubectlError("", "kubectl get pods")
		assert.Equal(t, "", wrapped)
	})
}
