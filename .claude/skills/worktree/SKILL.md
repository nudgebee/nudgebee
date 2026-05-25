---
description: Create a git worktree from a target branch with a new branch name
user-invocable: true
allowed-tools:
  - Bash
---

# Create Git Worktree

Create a new git worktree checked out from a target branch. Arguments: `$ARGUMENTS` should be in the format `<target-branch> <new-branch-name>`.

Examples:
- `/worktree main feature/add-auth`
- `/worktree prod hotfix/fix-crash`
- `/worktree test fix/flaky-tests`

## Step 1: Parse Arguments

Extract from `$ARGUMENTS`:
- **Target branch** (first arg): The base branch to create the worktree from (e.g., `main`, `test`, `prod`)
- **New branch name** (second arg): The name for the new branch in the worktree

If arguments are missing, ask the user to provide them in the format: `/worktree <target-branch> <new-branch-name>`

## Step 2: Fetch Latest

```bash
git fetch origin
```

## Step 3: Determine Worktree Path

Place the worktree as a sibling directory to the current repo:

```bash
# If repo is at /Users/user/work/nudgebee/nudgebee
# Worktree goes to /Users/user/work/nudgebee/nudgebee-<new-branch-name>
REPO_ROOT=$(git rev-parse --show-toplevel)
WORKTREE_DIR="${REPO_ROOT}-$(echo '<new-branch-name>' | tr '/' '-')"
```

The branch name's slashes are converted to dashes for the directory name (e.g., `feature/add-auth` becomes `nudgebee-feature-add-auth`).

## Step 4: Create the Worktree

```bash
git worktree add -b <new-branch-name> "$WORKTREE_DIR" origin/<target-branch>
```

This creates a new branch `<new-branch-name>` based on `origin/<target-branch>` and checks it out in the worktree directory.

## Step 5: Verify and Output

```bash
# Show all worktrees
git worktree list
```

Output:

```
Worktree created:
  Path:   {worktree_dir}
  Branch: {new-branch-name}
  Based on: origin/{target-branch}

To start working:
  cd {worktree_dir}

To remove later:
  git worktree remove {worktree_dir}
```
