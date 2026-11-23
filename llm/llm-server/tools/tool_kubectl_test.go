package tools

import (
	"encoding/json"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestContainerDefaultWarning guards against a real production miss: a
// multi-container pod with no default-container annotation silently returns
// only one container's logs, and — before this warning existed — nothing in
// the observation told the LLM another container was never checked. See
// namespace-233's cart-cache-test repro: the agent concluded "no issues" off
// a single clean container while the other one carried the actual warning.
func TestContainerDefaultWarning(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   string // "" means no warning expected; non-empty checked via Contains
	}{
		{"no stderr", "", ""},
		{"stderr with unrelated noise only", "Warning: some other notice", ""},
		{
			"multi-container defaulted — warns and names the others",
			`Defaulted container "cache" out of: cache, redis`,
			"redis",
		},
		{
			"single-container pod — kubectl never emits 'out of' with one name, nothing to warn about",
			`Defaulted container "app" out of: app`,
			"",
		},
		{
			"three containers — lists every one not fetched",
			`Defaulted container "main" out of: main, sidecar, exporter`,
			"sidecar",
		},
		{
			"CRLF line ending and irregular spacing — names parsed without stray whitespace",
			"Defaulted container \"cache\" out of: cache,  redis \r\n",
			"(redis)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := containerDefaultWarning(tc.stderr)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tc.want)
			assert.Contains(t, got, "does not mean the pod is healthy")
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
			assert.Equal(t, tc.want, KubectlNamespace(tc.command))
		})
	}
}

func TestInferKubectlVerbType(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  core.ToolRequestType
	}{
		{"incomplete describe remains read", "kubectl describe pods -n nudgebee -l", core.ToolRequestTypeRead},
		{"json wrapped describe", `{"command":"kubectl describe pods -n nudgebee -l"}`, core.ToolRequestTypeRead},
		{"port-forward is read", "kubectl port-forward svc/my-svc 8080:80", core.ToolRequestTypeRead},
		{"proxy is read", "kubectl proxy --port=8001", core.ToolRequestTypeRead},
		{"global flags before read verb", "kubectl --context prod -n nudgebee get pods", core.ToolRequestTypeRead},
		{"read config current-context", "kubectl config current-context", core.ToolRequestTypeRead},
		{"read config get-contexts", "kubectl config get-contexts", core.ToolRequestTypeRead},
		{"read config get-clusters", "kubectl config get-clusters", core.ToolRequestTypeRead},
		{"read config view", "kubectl config view --minify", core.ToolRequestTypeRead},
		{"read rollout status deployment/api", "kubectl rollout status deployment/api", core.ToolRequestTypeRead},
		{"read rollout history deployment/api", "kubectl rollout history deployment/api", core.ToolRequestTypeRead},
		{"read auth can-i update deployments", "kubectl auth can-i update deployments", core.ToolRequestTypeRead},
		{"read --context prod auth can-i patch pods", "kubectl --context prod auth can-i patch pods", core.ToolRequestTypeRead},
		{"fallback config set-context prod", "kubectl config set-context prod", ""},
		{"fallback config use-context prod", "kubectl config use-context prod", ""},
		{"fallback rollout restart deployment/api", "kubectl rollout restart deployment/api", ""},
		{"fallback rollout undo deployment/api", "kubectl rollout undo deployment/api", ""},
		{"fallback auth reconcile -f roles.yaml", "kubectl auth reconcile -f roles.yaml", ""},
		{"fallback config --unknown get-contexts", "kubectl config --unknown get-contexts", ""},
		{"fallback flag before config view", "kubectl config --kubeconfig=foo view", ""},
		{"create", "kubectl create deployment api --image=nginx", core.ToolRequestTypeCreate},
		{"update", "kubectl scale deployment api --replicas=2", core.ToolRequestTypeUpdate},
		{"delete", "kubectl delete pod api-123 -n nudgebee", core.ToolRequestTypeDelete},
		{"mutating command help is read", "kubectl delete pod --help", core.ToolRequestTypeRead},
		{"quoted help token does not turn update into read", `kubectl annotate pod api note='run -h now'`, core.ToolRequestTypeUpdate},
		{"context-dependent exec falls through", "kubectl exec api-123 -- touch /tmp/x", ""},
		{"exec payload help flag still falls through", "kubectl exec api-123 -- sh -h", ""},
		// #38525: kubectl exec had no static classifier at all, so every one
		// of these fell to an under-anchored LLM prompt that failed closed to
		// "update" — these are the literal dev examples from the issue.
		{"exec nc zero-io scan is read", "kubectl exec client -n test-ns-42e -- nc -z -v backend-service 80", core.ToolRequestTypeRead},
		{"exec nc without -z falls through", "kubectl exec client -n test-ns-42e -- nc backend-service 80", ""},
		{"exec ping is read", "kubectl exec client -n test-ns-42e -- ping -c 2 10.64.20.182", core.ToolRequestTypeRead},
		{"exec ls is read", "kubectl exec pod -n namespace-73a -- ls", core.ToolRequestTypeRead},
		{"exec curl GET is read", "kubectl exec pod -n ns -- curl -s http://llm-gateway:8000/rpc/config/settings/get", core.ToolRequestTypeRead},
		{"exec curl POST falls through", "kubectl exec pod -n ns -- curl -X POST http://llm-gateway:8000/x", ""},
		{"exec wget GET is read", "kubectl exec vmsingle-x -n victoria -- wget -qO- http://localhost:8429/api/v1/query", core.ToolRequestTypeRead},
		{"exec wget post-data falls through", "kubectl exec pod -n ns -- wget --post-data=x http://x", ""},
		{"exec du is read", "kubectl exec pod -n ns -- du -sh /usr/share/elasticsearch/data", core.ToolRequestTypeRead},
		{"exec grep is read", "kubectl exec pod -n ns -- grep ERROR app.log", core.ToolRequestTypeRead},
		{"exec mutating payload falls through", "kubectl exec pod -n ns -- rm -rf /data", ""},
		// Review (#38727): on main a static "read" skips the LLM check, so these must ask.
		{"exec env with a command falls through", "kubectl exec pod -n ns -- env rm -rf /data", ""},
		{"exec bare env is read", "kubectl exec pod -n ns -- env", core.ToolRequestTypeRead},
		{"exec curl -o falls through", "kubectl exec pod -n ns -- curl -s http://x -o /data/overwrite", ""},
		{"exec wget saving a file falls through", "kubectl exec pod -n ns -- wget http://x/big.tar", ""},
		{"exec date -s falls through", "kubectl exec pod -n ns -- date -s 2020-01-01", ""},
		{"exec rabbitmqctl list_exchanges is read", "kubectl exec rabbit-0 -n ns -- rabbitmqctl list_exchanges", core.ToolRequestTypeRead},
		{"exec rabbitmqctl eval falls through", "kubectl exec rabbit-0 -n ns -- rabbitmqctl eval 'halt().'", ""},
		{"exec curl readyz probe is read", "kubectl exec pod -n ns -- curl -s -i http://10.64.1.54:8080/readyz", core.ToolRequestTypeRead},
		{"unknown verb falls through", "kubectl frobnicate pods", ""},
		{"missing kubectl prefix falls through", "describe pods -n nudgebee", ""},
		{"other shell command falls through", "rm -rf /tmp/work", ""},
		{"pipeline falls through", "kubectl get pods | grep api", ""},
		{"compound read then delete falls through", "kubectl get pods && kubectl delete pod api", ""},
		{"redirect falls through", "kubectl get pods > pods.txt", ""},
		{"command substitution falls through", "kubectl get pods -l \"app=$(cat selector)\"", ""},
		{"quoted jsonpath operator remains read", `kubectl get pods -o 'jsonpath={.items[?(@.status.phase=="Running")]}'`, core.ToolRequestTypeRead},
		{"quoted pipe remains read", `kubectl get pods -o 'jsonpath={.items[*].metadata.name}{"|"}'`, core.ToolRequestTypeRead},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, InferKubectlVerbType(extractCommandFromToolInput(tt.input)))
		})
	}
}

// #41201: every kubectl config command is refused when it runs, so the
// approval check reads it; a write chained after one still needs approval.
func TestKubectlExecuteInferToolRequestType_ConfigIsRead(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	for input, want := range map[string]core.ToolRequestType{
		"kubectl config use-context prod":                                 core.ToolRequestTypeRead,
		"kubectl config set-context --current --namespace=shop":           core.ToolRequestTypeRead,
		"kubectl --request-timeout 5s config use-context prod":            core.ToolRequestTypeRead,
		"kubectl config use-context prod && kubectl get pods -n shop":     core.ToolRequestTypeRead,
		"kubectl config use-context prod && kubectl delete pod x -n shop": core.ToolRequestTypeDelete,
		"kubectl get configmap config -n shop":                            core.ToolRequestTypeRead,
		"kubectl rollout restart deployment config -n shop":               "",
	} {
		got, err := KubectlExecuteTool{}.InferToolRequestType(ctx, "kubectl_execute", input)
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
	}
	assert.Empty(t, InferKubectlVerbType("kubectl config use-context prod"), "the proxy's access check is unchanged")
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
		{"dot segment path obfuscation", "kubectl exec mypod -- cat /var/run/./secrets/kubernetes.io/serviceaccount/token", true},
		{"parent segment path obfuscation", "kubectl exec mypod -- cat /var/run/tmp/../secrets/token", true},
		{"wildcard path obfuscation", "kubectl exec mypod -- sh -c \"cat /var/run/se*rets/token\"", true},
		{"cp path normalization", "kubectl cp mypod:/var/run/./secrets/token /tmp/token", true},
		// Globs that could expand to a secret path.
		{"glob on the way to a secret root", "kubectl exec mypod -- ls /var/r*", true},
		{"glob at the filesystem root", "kubectl exec mypod -- grep -r token /*", true},
		{"single-character glob in a secret root", "kubectl cp mypod:/run/se?rets/x /tmp/x", true},
		{"glob through /proc", "kubectl exec mypod -- cat /proc/*/root/var/run/se*/x", true},
		{"glob naming the service-account mount", "kubectl exec mypod -- cat */serviceaccount/token", true},
		{"relative glob after cd toward a secret root", "kubectl exec mypod -- sh -c \"cd /var/run && cat s*/k*/s*/token\"", true},
		{"relative glob after relative cd", "kubectl exec mypod -- sh -c \"cd ../.. && cat va*/r*/s*/x\"", true},
		{"relative glob from a workdir of /", "kubectl exec p -- cat v*/run/s*/x", true},
		{"relative glob climbing with ..", "kubectl exec p -- cat ../../../../v*/run/s*/x", true},
		{"glob hiding .. after a safe prefix", "kubectl exec p -- cat /tmp/*/../../var/run/s*/x", true},
		{"glob from the home directory", "kubectl exec p -- sh -c 'cat ~/../../v*/run/s*/x'", true},
		{"relative glob after chroot", "kubectl exec p -- sh -c 'chroot / cat v*/run/s*/x'", true},
		{"relative glob after tar -C", "kubectl exec p -- sh -c 'tar -C/ -cf - s*/k*'", true},
		{"glob naming secrets", "kubectl exec p -- sh -c \"find / -path '*/secrets/*'\"", true},
		{"bare glob from a workdir of /", "kubectl exec p -- grep -r token *", true},
		{"brace expansion before a glob", "kubectl exec p -- sh -c 'cat /var/run/se{c,}rets/*/x'", true},
		{"variable as the glob's path", "kubectl exec p -- sh -c 'd=/var/run; cat $d/s*/x'", true},
		{"dot glob that can match ..", "kubectl exec p -- cat .?/v*/x", true},
		{"path disguised with a URL-like tail", "kubectl exec p -- cat /v*/run/s*/x://", true},
		{"command hidden behind a URL prefix", "kubectl exec p -- sh -c \"http://x;cat${IFS}/var/run/se*rets/token\"", true},
		// Globs that cannot reach one (false positives seen on dev).
		{"redis key pattern", `kubectl exec -n redis redis-master-0 -c redis -- sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli --scan --pattern "mcp:sess:*"'`, false},
		{"question mark in a URL query", `kubectl exec vmsingle-0 -n victoria -- sh -c 'wget -qO- "http://localhost:8429/api/v1/status/tsdb?topN=10"'`, false},
		{"PromQL in a URL", `kubectl exec prom-0 -- wget -qO- 'http://localhost:9090/api/v1/query?query=sum+by+(namespace)+(increase(x{job="a"}[5m]))'`, false},
		{"glob in a data directory", "kubectl exec -n shop cart-cache-0 -- du -sh /data/*", false},
		{"glob in an app directory", "kubectl exec postgresql-0 -c postgresql -- du -sh /bitnami/postgresql/* /bitnami/postgresql/data/*", false},
		{"glob under /var that cannot reach a secret root", "kubectl exec db-0 -- ls /var/lib/postgresql/data/*", false},
		{"relative glob after cd elsewhere", "kubectl exec mypod -- sh -c \"cd /tmp && ls *.log\"", false},
		{"path glued to a short flag", "kubectl exec p -- grep -f/var/run/se*/token x", true},
		{"redis CONFIG GET with a bare glob", "kubectl exec -it cart-cache-0 -n shop -- redis-cli CONFIG GET *", false},
		{"bare glob read recursively", "kubectl exec p -- grep -rl token *", true},
		{"bare glob walked by find", "kubectl exec p -- find * -name token", true},
		{"relative glob that cannot name a top-level directory", "kubectl exec mypod -- ls -la s*/token *.log", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, kubectlReadsSecretFilesystemPath(tc.command))
		})
	}
}

func TestValidateKubectlCommandAccess(t *testing.T) {
	blocked := []string{
		`kubectl get secret -n nudgebee-oss postgresql -o json | jq '.data | map_values(@base64d)'`,
		`kubectl get secret -n nudgebee -o json | jq '.items[].metadata.name'`,
		`kubectl get pods && kubectl get secrets -A`,
		`kubectl exec api -- cat /var/run/secrets/kubernetes.io/serviceaccount/token`,
		`kubectl exec api -- cat /var/run/./secrets/token`,
		`kubectl exec api -- sh -c "cat /var/run/se*rets/token"`,
		`kubectl get pods | cat /var/run/secrets/kubernetes.io/serviceaccount/token`,
		`kubectl get pods | grep -e. /var/run/./secrets/kubernetes.io/serviceaccount/token`,
		`kubectl cp local.txt api:/tmp/local.txt`,
		`kubectl --context k8s-dev cp api:/tmp/result.txt result.txt`,
	}
	for _, command := range blocked {
		require.Error(t, validateKubectlCommandAccess(command), command)
	}

	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods -A -o json | jq '.items[].metadata.name'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods -o 'jsonpath={$.items[*].metadata.name}'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods -o json | jq '.items | length'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | grep 'foo bar'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | grep -i nginx`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | grep -v Running`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | grep -E 'a|b'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | grep -F nginx`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods -o json | jq -e '.items'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods -o json | jq -r '.items[].metadata.name'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | head -20`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | wc -l`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | grep -A 20 nginx`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl describe nodes | grep -A 5 "Allocated resources"`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get events -n ns | tail -n 20`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods -A | head -n 20`))
	// sort and uniq pipeline filters
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | sort`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | sort -r`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | sort -n`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | sort -u`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | sort -k 2`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | sort -k2`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | uniq -c`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | uniq -i`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | sort | uniq -c`))
	// awk, cut, tr, and sed pipeline filters
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods -A --no-headers -o "custom-columns=NAMESPACE:.metadata.namespace" | sort | uniq -c | awk '{print $2"\t"$1}' | sort`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | awk '{print $1}'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | awk '{print $2"\t"$1}'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | awk -F, '{print $1}'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | awk -v OFS='\t' '{print $1, $2}'`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | cut -d' ' -f1`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | tr -s ' '`))
	require.NoError(t, validateKubectlCommandAccess(`kubectl get pods | sed 's/foo/bar/'`))
}

func TestValidateKubectlCommandAccess_Pipelines(t *testing.T) {
	pipelines := []string{
		// User's exact 5-stage pipeline
		`kubectl get pods -A --no-headers -o "custom-columns=NAMESPACE:.metadata.namespace" | sort | uniq -c | awk '{print $2"\t"$1}' | sort`,
		// Real-world multi-stage inspection pipelines
		`kubectl get nodes -o wide | awk '$2=="NotReady" {print $1}'`,
		`kubectl get pods | grep -v Running | grep -v Completed`,
		`kubectl get pods | column -t`,
		`kubectl get pods | sed 's/nudgebee-//'`,
		`kubectl get events -A --sort-by=.metadata.creationTimestamp | tail -n 50 | head -n 10`,
		`kubectl get pods -n kube-system --no-headers | wc -l`,
		`kubectl get nodes -o json | jq '.items[].status.addresses[] | select(.type=="InternalIP") | .address'`,
		`kubectl get pods | tr '[:upper:]' '[:lower:]'`,
		`kubectl get pods | cut -d' ' -f1 | sort -u`,
	}
	for _, pipe := range pipelines {
		require.NoError(t, validateKubectlCommandAccess(pipe), pipe)
	}
}

func TestValidateKubectlCommandAccess_SecretExclusion(t *testing.T) {
	secretCommands := []string{
		// Secret-bearing kinds
		`kubectl get secrets`,
		`kubectl get secret`,
		`kubectl get secret my-secret`,
		`kubectl describe secret my-secret`,
		`kubectl get secrets -A -o json`,
		`kubectl get sealedsecrets -A`,
		`kubectl get externalsecrets -n prod`,
		`kubectl get secretstores`,
		`kubectl get clustersecretstores`,
		`kubectl get secretproviderclasses`,
		`kubectl get secrets.v1.core`,
		// Mounted secret filesystem paths
		`kubectl exec api -- cat /var/run/secrets/kubernetes.io/serviceaccount/token`,
		`kubectl exec api -- sh -c "cat /run/secrets/token"`,
		`kubectl cp api:/var/run/secrets/token /tmp/token`,
		`kubectl cp local.txt api:/tmp/local.txt`,
		`kubectl --context k8s-dev cp api:/tmp/result.txt result.txt`,
		`kubectl attach api -- cat /var/lib/kubelet/pods/uuid/volumes/kubernetes.io~secret/token`,
		// Path obfuscations
		`kubectl exec api -- cat /var/run/./secrets/token`,
		`kubectl exec api -- cat /var/run/tmp/../secrets/token`,
		`kubectl exec api -- cat /var/run/sec""rets/token`,
		`kubectl exec api -- cat /var/run/se\crets/token`,
		`kubectl exec api -- sh -c "cat /var/run/se*rets/token"`,
		// Secret paths inside pipeline filters
		`kubectl get pods | cat /var/run/secrets/kubernetes.io/serviceaccount/token`,
		`kubectl get pods | grep -e. /var/run/secrets/token`,
	}
	for _, cmd := range secretCommands {
		err := validateKubectlCommandAccess(cmd)
		require.Error(t, err, cmd)
		require.Contains(t, err.Error(), "blocked", cmd)
	}
}

// Commands blocked on dev as secret-path reads because they held a glob
// character; they read no secret path and pass the relay boundary.
func TestValidateKubectlRelayCommandAllowsGlobsThatCannotReachSecrets(t *testing.T) {
	for _, command := range []string{
		`kubectl exec -n redis redis-master-0 -c redis -- sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli --scan --pattern "mcp:sess:*"'`,
		`kubectl exec vmsingle-0 -n victoria -- sh -c 'wget -qO- "http://localhost:8429/api/v1/status/tsdb?topN=10"'`,
		`kubectl exec -n shop cart-cache-0 -- du -sh /data/*`,
	} {
		assert.NoError(t, ValidateKubectlRelayCommand(command), command)
	}
	assert.Error(t, ValidateKubectlRelayCommand(`kubectl exec api -- sh -c "cat /var/run/se*rets/token"`))
}

func TestValidateKubectlRelayCommand(t *testing.T) {
	blocked := []string{
		// Metacharacters and compound execution
		`kubectl get pods; rm -rf /`,
		`kubectl get pods && rm -rf /`,
		`kubectl get pods | sort`,
		`kubectl get pods > /tmp/out`,
		`kubectl get pods < /tmp/in`,
		// Non-kubectl commands
		`helm list`,
		`bash -c "kubectl get pods"`,
		// Secret-bearing kinds
		`kubectl get secrets`,
		`kubectl get secret my-secret`,
		`kubectl describe sealedsecret foo`,
		`kubectl get externalsecrets`,
		// Secret paths
		`kubectl exec api -- cat /var/run/secrets/kubernetes.io/serviceaccount/token`,
		`kubectl cp api:/var/run/secrets/token /tmp/token`,
		// Expansions
		`kubectl get $(echo pods)`,
		"kubectl get `echo pods`",
	}
	for _, command := range blocked {
		require.Error(t, ValidateKubectlRelayCommand(command), command)
	}

	allowed := []string{
		`kubectl get pods -A`,
		`kubectl describe nodes`,
		`kubectl get deployments -n prod -o json`,
		`kubectl logs api-123 -n default --tail 500`,
		`kubectl get pods -o "custom-columns=NAME:.metadata.name"`,
	}
	for _, command := range allowed {
		require.NoError(t, ValidateKubectlRelayCommand(command), command)
	}
}

// A --raw API path reaches Secrets without naming the kind as a positional,
// so the path itself is checked.
func TestValidateKubectlRelayCommandRawAPIPaths(t *testing.T) {
	blocked := []string{
		`kubectl get --raw /api/v1/namespaces/x/secrets`,
		`kubectl get --raw /api/v1/namespaces/x/secrets/db-creds`,
		`kubectl get --raw=/api/v1/namespaces/x/secrets`,
		`kubectl get --raw /api/v1/secrets`,
		`kubectl get --raw /api/v1/watch/namespaces/x/secrets`,
		`kubectl get --raw /api/v1/namespaces/x/secrets/`,
		`kubectl get --raw //api//v1//namespaces/x//secrets`,
		`kubectl get --raw /api/v1/namespaces/x/pods/../secrets`,
		`kubectl get --raw /api/v1/namespaces/x/%73ecrets`,
		`kubectl get --raw /api/v1/namespaces/x/%2573ecrets`,
		`kubectl get --raw /api/v1/namespaces/x/Secrets`,
		`kubectl get --raw "/api/v1/namespaces/x/secrets?limit=1"`,
		`kubectl get --raw '/api/v1/namespaces/x/sec""rets'`,
		`kubectl --context dev get --raw /api/v1/namespaces/x/secrets`,
		`kubectl get --raw /apis/bitnami.com/v1alpha1/namespaces/x/sealedsecrets`,
		`kubectl get --raw /apis/external-secrets.io/v1beta1/namespaces/x/externalsecrets`,
		`kubectl get --raw /apis/external-secrets.io/v1beta1/namespaces/x/secretstores`,
		`kubectl get --raw /apis/external-secrets.io/v1beta1/clustersecretstores`,
		`kubectl get --raw /apis/secrets-store.csi.x-k8s.io/v1/namespaces/x/secretproviderclasses`,
		`kubectl create --raw /api/v1/namespaces/x/secrets -f secret.json`,
		`kubectl delete --raw /api/v1/namespaces/x/secrets/db-creds`,
	}
	for _, command := range blocked {
		require.Error(t, ValidateKubectlRelayCommand(command), command)
	}

	allowed := []string{
		`kubectl get --raw /api/v1/namespaces/x/pods`,
		`kubectl get --raw /healthz`,
		`kubectl get --raw /metrics`,
		`kubectl get --raw /apis/apps/v1/namespaces/x/deployments`,
		`kubectl get --raw /apis/secrets-store.csi.x-k8s.io/v1`,
		// Short names are not API path segments; namespaces often use them.
		`kubectl get --raw /api/v1/namespaces/es/pods`,
		`kubectl get --raw /api/v1/namespaces/ss/pods`,
	}
	for _, command := range allowed {
		require.NoError(t, ValidateKubectlRelayCommand(command), command)
	}
}

func TestKubectlCopyIsBlockedAtToolAndRelayBoundaries(t *testing.T) {
	commands := []string{
		`kubectl cp local.txt api:/tmp/local.txt`,
		`kubectl --context k8s-dev cp api:/tmp/result.txt result.txt`,
	}
	for _, command := range commands {
		require.ErrorContains(t, validateKubectlCommandAccess(command), "local paths resolve", command)
		require.ErrorContains(t, ValidateKubectlRelayCommand(command), "local paths resolve", command)
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

	t.Run("no specific match, raw carries CLI signal: generic recovery hint fires", func(t *testing.T) {
		// Post the generic-fallback fold (2026-07-03): unrecognized errors
		// that still carry CLI signal (`unknown`, `invalid`, `usage`, etc.)
		// get the universal "re-read the raw output before switching" nudge
		// with a kubectl <command> --help ref, instead of raw passthrough.
		// This is the intended new behavior; the raw error still round-trips
		// under original_error.
		raw := `error: unknown resource type "podz". Did you mean "pods"?`
		wrapped := wrapKubectlError(raw, "kubectl get podz")
		var env map[string]string
		err := json.Unmarshal([]byte(wrapped), &env)
		assert.NoError(t, err)
		assert.Contains(t, env["error_hint"], "re-read the raw output")
		assert.Contains(t, env["error_hint"], "kubectl <command> --help")
		assert.Equal(t, raw, env["original_error"])
	})

	t.Run("resource-NotFound: no CLI-flag signal, raw passthrough preserved", func(t *testing.T) {
		// A plain "pods not found" error has no CLI-flag signal (unknown flag,
		// usage, invalid arg, etc.) — the model just needs to change the
		// resource name, and neither the specific hint nor the generic
		// fallback adds anything. Raw passthrough is intentional here.
		raw := `Error from server (NotFound): pods "missing-pod": not found`
		wrapped := wrapKubectlError(raw, "kubectl get pod missing-pod")
		assert.Equal(t, raw, wrapped)
	})

	t.Run("truly opaque error (no CLI signal): raw passthrough preserved", func(t *testing.T) {
		raw := "connection reset by peer"
		wrapped := wrapKubectlError(raw, "kubectl get pods")
		assert.Equal(t, raw, wrapped)
	})

	t.Run("empty raw: pass-through empty", func(t *testing.T) {
		wrapped := wrapKubectlError("", "kubectl get pods")
		assert.Equal(t, "", wrapped)
	})
}

// kubectl_execute takes the shell shapes agents actually send, as aws_execute
// does. The fixtures were rejected on dev before; each kubectl call they make
// is still checked at the relay once the shell has expanded it.
func TestValidateKubectlCommandAccessAllowsShellShapes(t *testing.T) {
	for _, command := range []string{
		`kubectl get cm -n kube-system -l "app.kubernetes.io/name=ama-logs" -o name || kubectl get cm -n kube-system | grep -i ama`,
		`kubectl get pods -A | grep -iE "e2e|gateway" || true`,
		`kubectl get events -n shop --sort-by=.lastTimestamp 2>/dev/null | grep -i oom | tail -20`,
		`kubectl describe resourcequota/q -n shop && kubectl describe replicaset/r -n shop`,
		`kubectl get namespaces --no-headers -o custom-columns='NAME:.metadata.name' | grep -i nudgebee | ` +
			`while read ns; do printf '%s\t' "$ns"; kubectl get pods -n "$ns" --no-headers 2>/dev/null | wc -l; done`,
		`kubectl exec -n shop $(kubectl get pod -n shop -l app=api -o name) -c nginx -- nginx -V`,
	} {
		require.NoError(t, validateKubectlCommandAccess(command), command)
	}
}

// Building a secret read at runtime no longer fails at the tool, so pin that
// the call it expands to is still refused where the workspace sends it.
func TestRelayRefusesSecretReadsBuiltAtRuntime(t *testing.T) {
	for _, expanded := range []string{
		`kubectl get secrets`,
		`kubectl get secret -n shop db -o json`,
	} {
		require.Error(t, ValidateKubectlRelayCommand(expanded), expanded)
	}
}

func TestKubectlTargetKind(t *testing.T) {
	cases := map[string]string{
		"kubectl patch deployment api -n shop -p {}":         "deployment",
		"kubectl patch -n shop deploy/api -p {}":             "deployment",
		"kubectl set resources deployments.apps api -n shop": "deployment",
		"kubectl rollout restart sts/db -n shop":             "statefulset",
		"kubectl patch resourcequota q -n shop":              "resourcequota",
		"kubectl exec -c app my-pod -n shop -- ls":           "pod",
		"kubectl apply -f deploy.yaml -n shop":               "",
		"kubectl":                                            "",
	}
	for cmd, want := range cases {
		require.Equal(t, want, KubectlTargetKind(cmd), cmd)
	}
}

// A chain's exit 1 may come from an earlier command, so only a single
// command ending in grep is reclassified as an empty match.
func TestIsNoMatchExitIgnoresChains(t *testing.T) {
	exit1 := &workspace.CommandFailure{StdErr: "exit status 1"}
	require.True(t, isNoMatchExit(exit1, `kubectl get pods | grep foo`))
	require.False(t, isNoMatchExit(exit1, `kubectl get ns nosuch 2>/dev/null && kubectl get pods | grep foo`))
	require.False(t, isNoMatchExit(exit1, `kubectl get pods | grep foo; kubectl delete pod x 2>/dev/null`))
}

// TestInjectKubectlAutoTail covers issue #40196: the tool caps an unbounded
// `kubectl logs` read, and the caller is entitled to know it happened.
func TestInjectKubectlAutoTail(t *testing.T) {
	t.Run("unbounded logs read is capped and reports it", func(t *testing.T) {
		got, tailed := injectKubectlAutoTail("kubectl logs pod/api -n shop")
		assert.True(t, tailed)
		assert.Equal(t, "kubectl logs pod/api -n shop --tail 500", got)
	})

	t.Run("cap lands before a pipe so the pipeline still parses", func(t *testing.T) {
		got, tailed := injectKubectlAutoTail("kubectl logs pod/api -n shop | grep -i error")
		assert.True(t, tailed)
		assert.Equal(t, "kubectl logs pod/api -n shop --tail 500 | grep -i error", got)
	})

	// A pipe inside a quoted argument is not a pipeline separator. Seeking the
	// first "|" byte cut there and put the flag inside the quotes.
	t.Run("a quoted pipe is not mistaken for a pipeline separator", func(t *testing.T) {
		got, tailed := injectKubectlAutoTail(`kubectl logs pod/api -n shop -c 'a|b'`)
		assert.True(t, tailed)
		assert.Equal(t, `kubectl logs pod/api -n shop -c 'a|b' --tail 500`, got)
	})

	t.Run("a quoted pipe before a real one does not move the split", func(t *testing.T) {
		got, tailed := injectKubectlAutoTail(`kubectl logs pod/api -n 'a|b' | grep -E "error|fatal"`)
		assert.True(t, tailed)
		assert.Equal(t, `kubectl logs pod/api -n 'a|b' --tail 500 | grep -E "error|fatal"`, got)
	})

	for _, bounded := range []string{
		"kubectl logs pod/api -n shop --tail 20",
		"kubectl logs pod/api -n shop --tail=20",
		"kubectl logs pod/api -n shop --since=1h",
		"kubectl logs pod/api -n shop --since-time=2026-10-19T04:00:00Z",
		"kubectl logs pod/api -n shop -f",
		"kubectl logs pod/api -n shop --follow",
	} {
		t.Run("already bounded is left alone: "+bounded, func(t *testing.T) {
			got, tailed := injectKubectlAutoTail(bounded)
			assert.False(t, tailed, "the caller set its own bound, so nothing was hidden")
			assert.Equal(t, bounded, got)
		})
	}

	// Only the kubectl stage can bound the read; `grep -f FILE` is a real
	// shape, and matching " -f" across the whole command read it as --follow
	// and let an unbounded read through uncapped.
	t.Run("a later pipeline stage cannot suppress the cap", func(t *testing.T) {
		got, tailed := injectKubectlAutoTail("kubectl logs pod/api -n shop | grep -f patterns.txt")
		assert.True(t, tailed)
		assert.Equal(t, "kubectl logs pod/api -n shop --tail 500 | grep -f patterns.txt", got)
	})

	t.Run("a bound on the kubectl stage still suppresses it", func(t *testing.T) {
		_, tailed := injectKubectlAutoTail("kubectl logs pod/api -n shop -f | grep x")
		assert.False(t, tailed, "--follow on the kubectl stage is the caller's own bound")
	})

	t.Run("a non-logs command is untouched", func(t *testing.T) {
		got, tailed := injectKubectlAutoTail("kubectl get pods -n shop")
		assert.False(t, tailed)
		assert.Equal(t, "kubectl get pods -n shop", got)
	})

	// "logs" has to be the subcommand. Substring-matching it broke any command
	// with a bare logs token: --tail on a `get` is an unknown flag, so a
	// working command came back as an error.
	for _, notLogs := range []string{
		"kubectl get pods -n logs",
		"kubectl describe pod api -n logs",
		"kubectl get deployment logs -n shop",
	} {
		t.Run("a bare logs token is not the verb: "+notLogs, func(t *testing.T) {
			got, tailed := injectKubectlAutoTail(notLogs)
			assert.False(t, tailed)
			assert.Equal(t, notLogs, got)
		})
	}

	// The bound is a flag, not a substring: a pod whose name happens to contain
	// one read as a caller-set limit and suppressed the cap entirely.
	t.Run("a pod name containing a flag spelling does not suppress the cap", func(t *testing.T) {
		got, tailed := injectKubectlAutoTail("kubectl logs my--tail-pod -n shop")
		assert.True(t, tailed)
		assert.Equal(t, "kubectl logs my--tail-pod -n shop --tail 500", got)
	})

	t.Run("a global flag before the verb still resolves it", func(t *testing.T) {
		_, tailed := injectKubectlAutoTail("kubectl -n shop logs pod/api")
		assert.True(t, tailed)
	})

	t.Run("the note names the cap it describes", func(t *testing.T) {
		assert.Contains(t, kubectlAutoTailNote, "--tail 500")
		assert.Contains(t, kubectlAutoTailNote, "partial read")
	})
}

// TestGetErrorLinesFromLogStringWithCoverage covers the other half of #40196:
// the condenser drops most of what it reads, and said so nowhere.
