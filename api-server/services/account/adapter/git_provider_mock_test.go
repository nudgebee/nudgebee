package adapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// recordingGitProvider is a test GitProvider that needs no live repo and no
// credentials, and never touches the network.
//
//   - Checkout builds a *local* git repository in a temp dir, seeded with the
//     files the test declares, so the real Commit logic has a working tree to
//     operate on. This replaces only the one networked step (the clone).
//   - Commit delegates to the real implementation, so the actual edit + commit
//     logic under test runs for real against the seeded repo.
//   - RaisePR records the request and returns a canned URL instead of pushing,
//     so the test can assert on the PR that *would* have been raised.
type recordingGitProvider struct {
	// seedFiles maps a repo-relative path to its contents; written into the
	// working tree and committed as the initial revision by Checkout.
	seedFiles map[string]string
	// real backs Commit so the production commit path is exercised verbatim.
	real GitProvider

	checkouts int
	raised    []raisedPR
}

// raisedPR captures the arguments of a RaisePR call so tests can assert that
// the correct pull request would have been opened.
type raisedPR struct {
	dir              string
	branchName       string
	updateExistingPR bool
	existingPRNumber int
	body             string
	resolverType     string
	title            string
}

func newRecordingGitProvider(seedFiles map[string]string) *recordingGitProvider {
	return &recordingGitProvider{seedFiles: seedFiles, real: NewGitProvider()}
}

// gitOutput runs a git command in dir and returns its trimmed stdout, for tests
// that assert on repository state (e.g. the commit produced by Commit).
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (m *recordingGitProvider) Checkout(_ AccountAdapterContext, _ ApplyRecommendationRequest, _ gitDetailFromDeployment) (string, error) {
	m.checkouts++
	dir, err := os.MkdirTemp("", "gitprovider-seed")
	if err != nil {
		return "", err
	}
	for rel, content := range m.seedFiles {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	// Make an initial commit so the production Commit step (`git checkout -b`)
	// has a HEAD to branch from. Identity/signing are pinned inline so the seed
	// does not depend on the runner's global git config.
	steps := [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.email=seed@nudgebee.test", "-c", "user.name=seed", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed"},
	}
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("seed git %v failed: %s: %w", args, string(out), err)
		}
	}
	return dir, nil
}

func (m *recordingGitProvider) Commit(ctx AccountAdapterContext, dir string, request ApplyRecommendationRequest, gitDetails gitDetailFromDeployment, updateExistingPR bool) (string, error) {
	return m.real.Commit(ctx, dir, request, gitDetails, updateExistingPR)
}

func (m *recordingGitProvider) RaisePR(_ AccountAdapterContext, dir, branchName string, _ gitDetailFromDeployment, updateExistingPR bool, existingPRNumber int, githubBody, resolverType, githubTitle string) (string, error) {
	m.raised = append(m.raised, raisedPR{
		dir:              dir,
		branchName:       branchName,
		updateExistingPR: updateExistingPR,
		existingPRNumber: existingPRNumber,
		body:             githubBody,
		resolverType:     resolverType,
		title:            githubTitle,
	})
	return "https://github.com/seed-org/seed-repo/pull/1", nil
}
