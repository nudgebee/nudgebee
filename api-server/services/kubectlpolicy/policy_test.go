package kubectlpolicy

import "testing"

func TestCheckSecretAccessRefuses(t *testing.T) {
	for _, cmd := range []string{
		"kubectl get secrets -A",
		"kubectl get secrets -A -o yaml",
		"kubectl get secret my-secret -n default -o jsonpath={.data}",
		"kubectl -n default get secret",
		"kubectl get Secrets",
		"kubectl get secret/db-creds",
		"kubectl get secrets.v1.",
		"kubectl get pods,secrets",
		"kubectl get -o yaml secret",
		`kubectl get "secret"`,
		`kubectl get sec""ret`,
		`kubectl get s\ecret`,
		"kubectl get secret;",
		"kubectl get $(echo secret)",
		"kubectl describe secret x",
		"kubectl get sealedsecrets -A",
		"kubectl get externalsecrets -A",
		"kubectl get es -A",
		"kubectl get ss -A",
		"kubectl get css",
		"kubectl get secretproviderclasses -A",
		"kubectl get spc -A",
		"kubectl delete secret x",
		"kubectl create secret generic x --from-literal=a=b",
		"kubectl patch secret x -p {}",
		"kubectl set env deployment/app --from=secret/db",
		"get secrets -A", // runbook cloud.k8s.cli sends commands without the kubectl prefix
		"/usr/local/bin/kubectl get secrets",
		"kubectl get --raw /api/v1/secrets",
		"kubectl get --raw /api/v1/namespaces/default/secrets/x",
		"kubectl get --raw=/api/v1/namespaces/default/secrets",
		"kubectl get --raw /api/v1/namespaces/default/%73ecrets",
		"kubectl get --raw /api/v1/namespaces/default/%2573ecrets",
		"kubectl get --raw /api/v1//namespaces/x/../x/Secrets",
		"kubectl get --raw /apis/external-secrets.io/v1beta1/externalsecrets",
		"kubectl create --raw /api/v1/namespaces/x/serviceaccounts/sa/token -f -",
		"kubectl get --raw /api/v1/namespaces/x/serviceaccounts/sa/./token",
		"kubectl get pods -o go-template-file=/tmp/x",
		"kubectl get pods -o jsonpath-file=/etc/hostname",
		"kubectl get pods -ojsonpath-file=/etc/hostname",
		"kubectl get pods --output=custom-columns-file=/x",
		"kubectl get pods -o templatefile=/x",
		"kubectl get pods --output go-template-file=/x",
		"kubectl exec pod -- cat /var/run/secrets/kubernetes.io/serviceaccount/token",
		"kubectl exec pod -- cat /run/secrets/x",
		"kubectl exec pod -- ls /var/lib/kubelet/pods",
		"kubectl get pods --token-file=/var/run/secrets/kubernetes.io/serviceaccount/token",
		"kubectl exec pod -- cat ../var/run/secrets/x",
		"kubectl exec pod -- cat /var/./run/../run/secrets/x",
		"kubectl create token default",
		"kubectl -n x create token sa",
	} {
		if err := CheckSecretAccess(cmd); err == nil {
			t.Errorf("CheckSecretAccess(%q) = nil, want refusal", cmd)
		}
	}
}

func TestCheckSecretAccessAllows(t *testing.T) {
	for _, cmd := range []string{
		"kubectl get pods -A",
		"kubectl get pods -n default -o yaml",
		"kubectl get pods -o jsonpath={.items[*].metadata.name}",
		"kubectl get pods -o go-template={{.metadata.name}}",
		"kubectl get pods -o custom-columns=NAME:.metadata.name",
		"kubectl get deploy secret-rotator -n default",
		"kubectl get pods -l app=secret-manager",
		"kubectl describe pod my-secret-pod",
		"kubectl logs deployment/api -n default --tail=1000 --all-containers=true",
		"kubectl logs secret-rotator-abc -n default",
		"kubectl get configmaps -A",
		"kubectl get sts -A",
		"kubectl get serviceaccounts -A",
		"kubectl get --raw /api/v1/namespaces/default/pods",
		"kubectl get --raw /readyz?verbose",
		"kubectl top pods -A",
		"kubectl rollout status deployment/api -n default",
		"kubectl create job x --image=busybox",
		"kubectl version",
		"kubectl api-resources",
		"kubectl scale deployment/api --replicas=2 -n default",
	} {
		if err := CheckSecretAccess(cmd); err != nil {
			t.Errorf("CheckSecretAccess(%q) = %v, want nil", cmd, err)
		}
	}
}
