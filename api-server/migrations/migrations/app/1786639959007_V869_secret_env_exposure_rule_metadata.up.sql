-- =====================================================
-- V869: Rule metadata for secret_env_exposure
-- =====================================================
-- Backs the new `secret_env_exposure` scanner (scan_orchestrator) with the
-- title / description / remediation text the recommendation detail panel
-- reads via GetRuleMetadataByName. Without this row the finding still shows
-- in the Kubernetes Best Practices table, but with a title-cased rule_name
-- and no remediation guidance.
--
-- cloud_provider stays NULL — this is a K8s rule, not a provider-merged one.
-- =====================================================

INSERT INTO public.recommendation_rule
  (rule_name, category, title, description, service_name, recommendations, mitigations, compliances, "references")
VALUES
  (
    'secret_env_exposure',
    'Configuration',
    'Kubernetes Secret Exposed as Environment Variable',
    'This workload injects a Kubernetes Secret into a container through an environment variable, rather than mounting it as a file. Environment variables are readable by every process in the container, are captured in crash dumps and process listings, are frequently picked up by logging and APM agents, and are visible to anyone who can describe the pod or exec into it. A Secret mounted as a file is readable only by processes that open it, can be permission-restricted, and is updated in place when the Secret rotates.',
    'Kubernetes',
    '["Mount the Secret as a volume and read it from the file path instead of injecting it with env.valueFrom.secretKeyRef or envFrom.secretRef. For secrets that need rotation without a pod restart, a file mount is the only option — environment variables are fixed at container start. For higher-value credentials, consider an external secret store (Vault, AWS Secrets Manager, Azure Key Vault, GCP Secret Manager) with a CSI driver or operator, so the value never lands in etcd as a Kubernetes Secret at all."]'::jsonb,
    '["Replace the environment variable with a volume mount:\n\nspec:\n  containers:\n    - name: app\n      volumeMounts:\n        - name: app-secret\n          mountPath: /etc/secrets\n          readOnly: true\n  volumes:\n    - name: app-secret\n      secret:\n        secretName: my-secret\n        defaultMode: 0400\n\nThen read the value from /etc/secrets/<key> in the application instead of from the environment."]'::jsonb,
    '["CIS Kubernetes Benchmark 5.4.1"]'::jsonb,
    '["https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets-as-files-from-a-pod", "https://kubernetes.io/docs/concepts/security/secrets-good-practices/"]'::jsonb
  )
ON CONFLICT (rule_name, COALESCE(cloud_provider, '')) DO NOTHING;
