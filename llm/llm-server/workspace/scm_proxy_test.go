package workspace

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

func TestCommandEnvBindsScmCommandsToTheProxy(t *testing.T) {
	executionTokenTestSetup(t)
	prevHost, prevURL, prevPort := config.Config.LlmServerWorkspaceProxyTLSHost, config.Config.LlmServerUrl, config.Config.LlmServerWorkspaceProxyTLSPort
	t.Cleanup(func() {
		config.Config.LlmServerWorkspaceProxyTLSHost, config.Config.LlmServerUrl, config.Config.LlmServerWorkspaceProxyTLSPort = prevHost, prevURL, prevPort
	})
	config.Config.LlmServerWorkspaceProxyTLSHost = ""
	config.Config.LlmServerUrl = "http://llm-server:8000"
	config.Config.LlmServerWorkspaceProxyTLSPort = 8443

	w := &workspaceManager{httpClient: &http.Client{Timeout: 60 * time.Second}}
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{"ws-acct"})
	caller := map[string]string{
		ENV_NB_SCM_GITHUB_TOOL:   "github_execute",
		ENV_NB_SCM_GITHUB_CONFIG: "acme-github",
		ENV_NB_SCM_WRITE:         "true",
		"GITHUB_TOKEN":           "real-token-must-not-survive",
	}

	env, err := w.commandEnv(ctx, "ws-acct", caller)
	require.NoError(t, err)

	for _, k := range []string{ENV_NB_SCM_GITHUB_TOOL, ENV_NB_SCM_GITHUB_CONFIG, ENV_NB_SCM_WRITE, "GITHUB_TOKEN", "GH_TOKEN", "GITLAB_HOST", "GITLAB_TOKEN"} {
		_, present := env[k]
		assert.False(t, present, "%s must not reach the pod", k)
	}
	assert.Equal(t, "llm-server:8443", env["GH_HOST"])
	assert.Equal(t, env[ENV_NB_WORKSPACE_TOKEN], env["GH_ENTERPRISE_TOKEN"], "gh authenticates with the command's own token")
	assert.Equal(t, "/etc/ssl/certs:"+workspaceProxyCADir, env["SSL_CERT_DIR"])

	claims := parseExecutionToken(t, env["GH_ENTERPRISE_TOKEN"])
	require.NotNil(t, claims.Scm)
	assert.Equal(t, ScmClaims{Github: &ScmBinding{Tool: "github_execute", Config: "acme-github"}, Write: true}, *claims.Scm)

	plain, err := w.commandEnv(ctx, "ws-acct", map[string]string{})
	require.NoError(t, err)
	assert.Nil(t, parseExecutionToken(t, plain[ENV_NB_WORKSPACE_TOKEN]).Scm, "other commands' tokens are not bound to an integration")
	_, hasHost := plain["GH_HOST"]
	assert.False(t, hasHost)
}

func TestCommandEnvBindsBothProvidersAndRejectsHalfBindings(t *testing.T) {
	executionTokenTestSetup(t)
	w := &workspaceManager{httpClient: &http.Client{Timeout: 60 * time.Second}}
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{"ws-acct"})

	env, err := w.commandEnv(ctx, "ws-acct", map[string]string{
		ENV_NB_SCM_GITHUB_TOOL:   "github_execute",
		ENV_NB_SCM_GITHUB_CONFIG: "acme-github",
		ENV_NB_SCM_GITLAB_TOOL:   "gitlab_execute",
		ENV_NB_SCM_GITLAB_CONFIG: "acme-gitlab",
		ENV_NB_SCM_WRITE:         "false",
		"GITLAB_TOKEN":           "real-token-must-not-survive",
	})
	require.NoError(t, err)
	token := env[ENV_NB_WORKSPACE_TOKEN]
	assert.Equal(t, token, env["GH_ENTERPRISE_TOKEN"], "gh and glab share the command's one token")
	assert.Equal(t, token, env["GITLAB_TOKEN"])
	assert.Equal(t, WorkspaceProxyTLSHost(), env["GITLAB_HOST"])
	assert.Equal(t, WorkspaceProxyTLSHost(), env["GH_HOST"])
	claims := parseExecutionToken(t, token).Scm
	require.NotNil(t, claims)
	assert.Equal(t, "acme-github", claims.Github.Config)
	assert.Equal(t, "acme-gitlab", claims.Gitlab.Config)
	assert.False(t, claims.Write)

	_, err = w.commandEnv(ctx, "ws-acct", map[string]string{ENV_NB_SCM_GITHUB_TOOL: "github_execute"})
	assert.ErrorContains(t, err, "incomplete scm proxy binding", "a half-marked command fails instead of running unbound")
}

func TestScrubTokenMasksTheCommandToken(t *testing.T) {
	token := "eyJhbGciOiJIUzI1NiJ9.payload.signature"
	assert.Equal(t, "GH_ENTERPRISE_TOKEN=[REDACTED]\n", scrubToken("GH_ENTERPRISE_TOKEN="+token+"\n", token))
	assert.Equal(t, "short", scrubToken("short", "tiny"), "an empty or tiny token never masks ordinary output")
}

func TestWorkspaceProxyCertificateChainsToItsCA(t *testing.T) {
	prevNs := config.Config.LlmServerCodeAgentNamespace
	t.Cleanup(func() { config.Config.LlmServerCodeAgentNamespace = prevNs })
	config.Config.LlmServerCodeAgentNamespace = "nudgebee"

	now := time.Now()
	caPEM, keyPEM, err := newWorkspaceProxyCA(now, workspaceProxyNames("llm-server"))
	require.NoError(t, err)
	ca, caKey, err := parseWorkspaceProxyCA(caPEM, keyPEM)
	require.NoError(t, err)
	assert.True(t, ca.IsCA)

	leaf, err := signWorkspaceProxyLeaf(ca, caKey, workspaceProxyNames("llm-server"), now)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"llm-server", "llm-server.nudgebee", "llm-server.nudgebee.svc", "llm-server.nudgebee.svc.cluster.local"}, cert.DNSNames)

	roots := x509.NewCertPool()
	block, _ := pem.Decode(caPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	roots.AddCert(caCert)
	for _, name := range []string{"llm-server", "llm-server.nudgebee.svc"} {
		_, err = cert.Verify(x509.VerifyOptions{DNSName: name, Roots: roots})
		assert.NoError(t, err, name)
	}
	_, err = cert.Verify(x509.VerifyOptions{DNSName: "api.github.com", Roots: roots})
	assert.Error(t, err, "the proxy certificate never covers GitHub's own names")

	assert.Equal(t, []string{"host.docker.internal"}, workspaceProxyNames("host.docker.internal"))
}

func TestWorkspaceProxyCAVolumeMountsOnlyThePublicCert(t *testing.T) {
	vol, mount := workspaceProxyCAVolume()
	require.NotNil(t, vol.Secret)
	assert.Equal(t, []string{workspaceProxyCAKey}, []string{vol.Secret.Items[0].Path})
	assert.Len(t, vol.Secret.Items, 1, "the CA key never reaches a workspace pod")
	assert.True(t, *vol.Secret.Optional)
	assert.True(t, mount.ReadOnly)
	assert.Empty(t, mount.SubPath, "no subPath, so a rotated CA reaches running pods")
}

func TestWorkspaceProxyHostnameAcceptsAHostWithoutPort(t *testing.T) {
	prev := config.Config.LlmServerWorkspaceProxyTLSHost
	t.Cleanup(func() { config.Config.LlmServerWorkspaceProxyTLSHost = prev })
	for host, want := range map[string]string{"scm.example.com": "scm.example.com", "scm.example.com:9443": "scm.example.com", "10.0.0.5:8443": "10.0.0.5"} {
		config.Config.LlmServerWorkspaceProxyTLSHost = host
		assert.Equal(t, want, workspaceProxyHostname(), host)
	}
}

func TestWorkspaceProxyCertificateIsResignedBeforeItExpires(t *testing.T) {
	now := time.Now()
	caPEM, keyPEM, err := newWorkspaceProxyCA(now, workspaceProxyNames("llm-server"))
	require.NoError(t, err)
	ca, caKey, err := parseWorkspaceProxyCA(caPEM, keyPEM)
	require.NoError(t, err)
	src := &workspaceProxyCertSource{ca: ca, caKey: caKey, names: []string{"llm-server"}}

	first, err := src.current(now)
	require.NoError(t, err)
	same, err := src.current(now.Add(30 * 24 * time.Hour))
	require.NoError(t, err)
	assert.Same(t, first, same, "a fresh certificate is reused")
	renewed, err := src.current(now.Add(workspaceProxyLeafValid - 6*24*time.Hour))
	require.NoError(t, err)
	assert.NotSame(t, first, renewed, "a week before expiry it is re-signed")
}

func TestWorkspaceProxyCAIsNameConstrained(t *testing.T) {
	prevNs := config.Config.LlmServerCodeAgentNamespace
	t.Cleanup(func() { config.Config.LlmServerCodeAgentNamespace = prevNs })
	config.Config.LlmServerCodeAgentNamespace = "nudgebee"
	now := time.Now()
	caPEM, keyPEM, err := newWorkspaceProxyCA(now, workspaceProxyNames("llm-server"))
	require.NoError(t, err)
	ca, caKey, err := parseWorkspaceProxyCA(caPEM, keyPEM)
	require.NoError(t, err)

	// A certificate the CA's key signs for another host does not verify.
	forged, err := signWorkspaceProxyLeaf(ca, caKey, []string{"api.github.com"}, now)
	require.NoError(t, err)
	assert.Error(t, verifyWorkspaceProxyLeaf(ca, forged, "api.github.com"), "a leaked CA key cannot impersonate GitHub")

	own, err := signWorkspaceProxyLeaf(ca, caKey, workspaceProxyNames("llm-server"), now)
	require.NoError(t, err)
	assert.NoError(t, verifyWorkspaceProxyLeaf(ca, own, "llm-server.nudgebee.svc"))

	other, err := signWorkspaceProxyLeaf(ca, caKey, []string{"host.docker.internal"}, now)
	require.NoError(t, err)
	assert.Error(t, verifyWorkspaceProxyLeaf(ca, other, "host.docker.internal"), "a CA made for one host never vouches for another")
}

func TestWorkspaceProxySecretNameFollowsTheProxyHost(t *testing.T) {
	prev := config.Config.LlmServerWorkspaceProxyTLSHost
	t.Cleanup(func() { config.Config.LlmServerWorkspaceProxyTLSHost = prev })
	config.Config.LlmServerWorkspaceProxyTLSHost = "llm-server:8443"
	inCluster := workspaceProxySecretName()
	config.Config.LlmServerWorkspaceProxyTLSHost = "LLM-Server:9443"
	assert.Equal(t, inCluster, workspaceProxySecretName(), "the port and case do not change the CA")
	config.Config.LlmServerWorkspaceProxyTLSHost = "host.docker.internal:8443"
	assert.NotEqual(t, inCluster, workspaceProxySecretName(), "another host gets its own CA")
	assert.Regexp(t, `^llm-server-workspace-proxy-[0-9a-f]{10}$`, workspaceProxySecretName())
}

func TestAddScmEnvKeepsACallersCertDirs(t *testing.T) {
	env := map[string]string{"SSL_CERT_DIR": "/opt/certs"}
	addScmEnv(env, &ScmClaims{Github: &ScmBinding{Tool: "github_execute", Config: "acme"}}, "token")
	assert.Equal(t, "/opt/certs:"+workspaceProxyCADir, env["SSL_CERT_DIR"])
}
