package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
