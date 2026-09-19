# Cross-account workspace execution: first delivery slice

## Scope

This slice moves Kubernetes target selection to the concrete CLI invocation
boundary and gives that boundary a short-lived, user-bound authorization
credential. It also provides a virtual, read-only kubeconfig view so scripts
can discover the clusters available to the caller.

## Contract

1. Every workspace command receives the generic short-lived
   `workspace-execute` token from the workspace manager. It carries the
   workspace account and the caller's reduced, tenant-scoped grants.
2. The `kubectl` shim sends the real process argv unchanged. llm-server removes
   `--context NAME` / `--context=NAME`, resolves the requested context, and
   reconstructs the relay command.
3. llm-server reconstructs the caller security context from signed grants and
   resolves the named cluster only from integrations that caller can read.
4. Kubernetes target authorization is enforced again at the proxy. A separate
   operation capability is still required before opaque scripts can safely be
   permitted to issue writes.
5. With no `--context`, execution continues to use `NB_TOOL_CONFIG_NAME`, then
   the existing workspace-account fallback. Existing target-bound tokens keep
   their current behavior.
6. `kubectl config current-context`, `get-contexts`, and `view` are answered
   from the caller's authorized integrations. Commands that mutate kubeconfig
   remain blocked.

## Adversarial pre-implementation pass

- **Stolen environment token:** the token expires with the command request and
  is bound to the originating workspace account and reduced user grants.
- **Target-name spoofing:** the proxy ignores client-supplied account ids for
  target selection and resolves the context name against the signed caller
  grants.
- **Parser bypass:** target extraction happens from the concrete argv sent by
  the shim, not from the outer shell program text. Both supported kubectl flag
  forms are removed by llm-server before relay execution.
- **Hidden writes in Python or shell:** target access is enforced, but operation
  approval is not yet encoded in the generic execution token. Add a read/write
  capability claim before this path is considered complete for opaque writes.
- **Default-path regression:** absence of a context preserves the existing
  config-name/account fallback, and legacy workspace tokens remain accepted.
- **Fake kubeconfig state:** only read-only discovery commands are virtualized;
  `use-context`, `set-context`, credential changes, and file-backed mutations
  fail explicitly.

Verdict: **PROCEED AFTER CAPABILITY BINDING**. Target authorization remains
server-side; operation authorization must also be bound to the short-lived
token before merge.

## Acceptance criteria

- A complex shell or Python script can invoke two kubectl commands with two
  authorized `--context` values in one `shell_execute` call.
- An inaccessible or unknown context fails closed and reports only authorized
  context names.
- The execution token's operation capability is enforced at the proxy; opaque
  writes without a write capability are rejected.
- `kubectl config get-contexts`, `current-context`, and `view` return useful
  virtual data; kubeconfig mutation remains rejected.
- A kubectl command without `--context` follows the pre-existing default path.
- Focused shim, proxy, workspace-token, and shell-tool tests pass.

## Live integration suite

The read-only live suite is in
`llm/llm-server/tools/cross_account_kubernetes_e2e_test.go`. It exercises the
deployed workspace, shim, llm-server proxy, and relay rather than mocks.

Choose two connected clusters the test user can read. Use the UID of the
`kube-system` namespace as the expected stable identity for each cluster. The
workspace account's saved/default Kubernetes config also needs its expected
UID. Optionally name a connected context the user cannot read to exercise the
negative case.

```bash
cd llm/llm-server

export CROSS_ACCOUNT_K8S_E2E=1
export TEST_TENANT=<tenant-id>
export TEST_USER=<user-id>
export CROSS_ACCOUNT_WORKSPACE_ACCOUNT=<workspace-account-id>

export CROSS_ACCOUNT_DEFAULT_CONTEXT=<saved-context-name>
export CROSS_ACCOUNT_DEFAULT_EXPECTED_UID=<kube-system-namespace-uid>

export CROSS_ACCOUNT_CONTEXT_1=<first-context-name>
export CROSS_ACCOUNT_ACCOUNT_1=<first-account-id>
export CROSS_ACCOUNT_EXPECTED_UID_1=<first-kube-system-namespace-uid>

export CROSS_ACCOUNT_CONTEXT_2=<second-context-name>
export CROSS_ACCOUNT_ACCOUNT_2=<second-account-id>
export CROSS_ACCOUNT_EXPECTED_UID_2=<second-kube-system-namespace-uid>

# Optional: a real connected context intentionally omitted from this user's grants.
export CROSS_ACCOUNT_UNAUTHORIZED_CONTEXT=<inaccessible-context-name>

go test -tags=e2e -v ./tools -run '^TestCrossAccountKubernetes' -count=1
```

The suite verifies:

1. One Python process can invoke kubectl against both authorized contexts.
2. Virtual kubeconfig discovery lists both contexts and hides the unauthorized one.
3. Direct use of an unauthorized context fails closed.
4. Omitting `--context` preserves the saved/default cluster behavior.
5. A kubectl failure keeps a non-zero tool result and actionable stderr.
