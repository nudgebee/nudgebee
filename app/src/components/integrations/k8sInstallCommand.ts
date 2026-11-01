/**
 * The shell command that installs the Kubernetes agent for an account, given the
 * token from `agents_create_token`. Shared by every place that hands out a fresh
 * token (the account row's "Re-new Token" and the pending-install reminder), so
 * the two can't drift apart.
 */
export const buildK8sInstallCommand = (accessKey: string, accessSecret: string): string =>
  `wget https://raw.githubusercontent.com/nudgebee/k8s-agent/main/installation.sh && bash installation.sh -a "${accessKey}:${accessSecret}"`;
