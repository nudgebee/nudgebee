package k8s_upgrade

import (
	"strings"
	"testing"
)

func TestValidateAndSanitizeKubectlCommandRefusesSecrets(t *testing.T) {
	for _, cmd := range []string{
		"kubectl get secrets -A",
		"kubectl get secret db -n prod -o yaml",
		"kubectl get --raw /api/v1/namespaces/prod/secrets",
		"kubectl get pods -o jsonpath-file=/var/run/secrets/kubernetes.io/serviceaccount/token",
		"kubectl describe externalsecrets -A",
	} {
		if _, err := validateAndSanitizeKubectlCommand(cmd); err == nil || !strings.Contains(err.Error(), "blocked") {
			t.Errorf("validateAndSanitizeKubectlCommand(%q) err = %v, want a secret-access refusal", cmd, err)
		}
	}
}

func TestValidateAndSanitizeKubectlCommandAllowsUpgradeCommands(t *testing.T) {
	for _, cmd := range []string{
		"kubectl get nodes -o wide",
		"kubectl get pods -A",
		"kubectl drain node-1 --ignore-daemonsets --delete-emptydir-data",
		"kubectl rollout status deployment/api -n prod",
	} {
		if _, err := validateAndSanitizeKubectlCommand(cmd); err != nil {
			t.Errorf("validateAndSanitizeKubectlCommand(%q) = %v, want nil", cmd, err)
		}
	}
}
