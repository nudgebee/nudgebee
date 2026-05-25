---
description: Automate the backmerge flow after hotfixes (prod -> test -> main)
user-invocable: true
allowed-tools:
  - Bash
  - Read
---

# Backmerge

Automate the backmerge chain after a hotfix or production merge. Optional argument: `$ARGUMENTS` (starting branch, defaults to `prod`).

## Branch Strategy Reference

```
prod ──PR──> test ──PR──> main
```

After a hotfix lands on `prod`, it must be backmerged through `test` into `main` to keep all branches in sync.

## Step 0: Validate State

```bash
git fetch origin
```

Check that the starting branch exists and has commits ahead of the target:

```bash
# Check prod -> test delta
git log --oneline origin/test..origin/prod | head -20

# Check test -> main delta (may be empty if prod hasn't merged to test yet)
git log --oneline origin/main..origin/test | head -20
```

If `origin/test..origin/prod` is empty, there's nothing to backmerge. Inform the user and stop.

## Step 1: Create PR from prod -> test

Check if a backmerge PR already exists:
```bash
gh pr list --base test --head prod --state open --json number,title,url
```

**If a PR already exists:** show it and ask if the user wants to proceed to the next step.

**If no PR exists:** create one:
```bash
gh pr create \
  --base test \
  --head prod \
  --title "chore(release): backmerge prod -> test" \
  --body "$(cat <<'EOF'
# Description

Automated backmerge of production changes into test branch.

### Commits included
$(git log --oneline origin/test..origin/prod)

## Type of change

- [x] Chore (non-breaking change which does not modify src or test files)

# How Has This Been Tested?

- [ ] Review for merge conflicts
- [ ] CI passes after merge

### Action required
- Review for conflicts
- Merge when ready
- Then backmerge test -> main (use `/backmerge` again)

EOF
)"
```

## Step 2: Wait for merge or prompt user

Ask the user:
- "PR #{number} created for prod -> test. Should I also create the test -> main PR now, or wait until the first one is merged?"

**If user says wait:** stop here, print the PR URL.
**If user says continue:** proceed to step 3.

## Step 3: Create PR from test -> main

Check if a backmerge PR already exists:
```bash
gh pr list --base main --head test --state open --json number,title,url
```

**If a PR already exists:** show it and stop.

**If no PR exists:** create one:
```bash
gh pr create \
  --base main \
  --head test \
  --title "chore(release): backmerge test -> main" \
  --body "$(cat <<'EOF'
# Description

Automated backmerge of test changes into main branch.

### Commits included
$(git log --oneline origin/main..origin/test)

## Type of change

- [x] Chore (non-breaking change which does not modify src or test files)

# How Has This Been Tested?

- [ ] Review for merge conflicts
- [ ] CI passes after merge

### Action required
- Review for conflicts
- Merge when ready

EOF
)"
```

## Step 4: Output Summary

```
Backmerge Status:
  1. prod -> test: PR #{number} ({state}) — {url}
  2. test -> main: PR #{number} ({state}) — {url}

Next steps:
  - Review and merge PRs in order (test first, then main)
  - Resolve any conflicts in the PRs
```
