package knowledge

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildRepoTarball(t *testing.T, files map[string]string, symlinks map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "acme-skills-abc123/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	for name, target := range symlinks {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "acme-skills-abc123/" + name, Linkname: target, Typeflag: tar.TypeSymlink}))
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return &buf
}

func TestDiscoverSkillsInTarball(t *testing.T) {
	md := func(name string) string {
		return "---\nname: " + name + "\ndescription: does " + name + "\n---\nbody of " + name
	}
	repo := buildRepoTarball(t, map[string]string{
		"README.md":                                 "not a skill",
		"skills/prometheus/promql/SKILL.md":         md("promql"),
		"skills/prometheus/promql/references/fn.md": "functions",
		"skills/prometheus/promql/scripts/check.sh": "echo ok",
		"skills/loki/SKILL.md":                      md("loki"),
		"skills/loki/nested/SKILL.md":               md("nested"),
		"skills/loki/nested/notes.md":               "belongs to nested",
		"skills/broken/SKILL.md":                    "no header",
		"other/outside/SKILL.md":                    md("outside"),
	}, map[string]string{"skills/loki/link.md": "/etc/passwd"})

	skills, skipped, err := DiscoverSkillsInTarball(repo, "skills")
	require.NoError(t, err)

	byPath := map[string]*SkillPackage{}
	for _, s := range skills {
		byPath[s.Meta.SourcePath] = s
	}
	require.Len(t, byPath, 3, "outside the folder is ignored")
	assert.Equal(t, map[string]string{"references/fn.md": "functions", "scripts/check.sh": "echo ok"}, byPath["skills/prometheus/promql"].Files)
	assert.Empty(t, byPath["skills/loki"].Files, "nested skill's files and the symlink are not loki's")
	assert.Equal(t, map[string]string{"notes.md": "belongs to nested"}, byPath["skills/loki/nested"].Files)
	assert.Equal(t, "body of promql", byPath["skills/prometheus/promql"].Body)
	assert.Equal(t, []SkippedSkill{{SourcePath: "skills/broken", Reason: "SKILL.md does not start with a --- header"}}, skipped)

	all, _, err := DiscoverSkillsInTarball(buildRepoTarball(t, map[string]string{"SKILL.md": md("root"), "x/SKILL.md": md("x")}, nil), "")
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "", all[0].Meta.SourcePath, "a repo can be one skill")
}

func TestDiscoverSkillsInTarball_NotGzip(t *testing.T) {
	_, _, err := DiscoverSkillsInTarball(bytes.NewBufferString("not an archive"), "")
	assert.ErrorContains(t, err, "read archive")
}

func TestDiscoverSkillsInTarball_LargeFileNotRead(t *testing.T) {
	repo := buildRepoTarball(t, map[string]string{
		"s/SKILL.md":        "---\nname: s\ndescription: d\n---\nb",
		"s/assets/huge.bin": strings.Repeat("x", 2<<20),
	}, nil)
	skills, _, err := DiscoverSkillsInTarball(repo, "")
	require.NoError(t, err)
	require.Len(t, skills, 1)
	assert.Empty(t, skills[0].Files)
	assert.Equal(t, []SkillFileInfo{{Path: "assets/huge.bin", Size: 2 << 20, Skipped: "larger than 1 MB"}}, skills[0].Meta.Files)
}

func TestCapReaderStopsAnOversizedDownload(t *testing.T) {
	r := &capReader{r: strings.NewReader(strings.Repeat("x", 2048)), left: 1024}
	b, err := io.ReadAll(r)
	assert.ErrorIs(t, err, errSkillSourceTooLarge)
	assert.Len(t, b, 1024)
	assert.True(t, r.over)

	ok := &capReader{r: strings.NewReader("small"), left: 1024}
	b, err = io.ReadAll(ok)
	require.NoError(t, err)
	assert.Equal(t, "small", string(b))
	assert.False(t, ok.over)

	// Exactly at the cap is not over it (review, #41652).
	exact := &capReader{r: strings.NewReader(strings.Repeat("x", 1024)), left: 1024}
	b, err = io.ReadAll(exact)
	require.NoError(t, err)
	assert.Len(t, b, 1024)
	assert.False(t, exact.over)
	// One byte over is.
	over := &capReader{r: strings.NewReader(strings.Repeat("x", 1025)), left: 1024}
	b, err = io.ReadAll(over)
	assert.ErrorIs(t, err, errSkillSourceTooLarge)
	assert.Len(t, b, 1024)
}

// A repo cut by the unpacked limit is an error wherever the cut lands, also on
// a file boundary where tar sees a clean EOF (review, #41652): a partial list
// would archive every skill after the cut.
func TestDiscoverSkillsInTarball_CutIsNeverAPartialRepo(t *testing.T) {
	md := func(name string) string { return "---\nname: " + name + "\ndescription: d\n---\nb" }
	repo := buildRepoTarball(t, map[string]string{"a/SKILL.md": md("a"), "b/SKILL.md": md("b"), "c/SKILL.md": md("c")}, nil)
	gz, err := gzip.NewReader(bytes.NewReader(repo.Bytes()))
	require.NoError(t, err)
	raw, err := io.ReadAll(gz)
	require.NoError(t, err)
	trailer := 1024 // two zero blocks end a tar
	old := skillSourceMaxUnpacked
	t.Cleanup(func() { skillSourceMaxUnpacked = old })
	for limit := 512; limit < len(raw)-trailer; limit += 512 {
		skillSourceMaxUnpacked = int64(limit)
		skills, _, err := DiscoverSkillsInTarball(bytes.NewReader(repo.Bytes()), "")
		require.Error(t, err, "limit %d returned %d skills", limit, len(skills))
	}
	skillSourceMaxUnpacked = old
	skills, _, err := DiscoverSkillsInTarball(bytes.NewReader(repo.Bytes()), "")
	require.NoError(t, err)
	assert.Len(t, skills, 3)
}

// A folder that no longer exists upstream fails the sync instead of looking
// like a source with no skills, which would archive them all (review, #41652).
func TestDiscoverSkillsInTarball_MissingFolder(t *testing.T) {
	repo := buildRepoTarball(t, map[string]string{"skills/a/SKILL.md": "---\nname: a\ndescription: d\n---\nb"}, nil)
	_, _, err := DiscoverSkillsInTarball(bytes.NewReader(repo.Bytes()), "skills-renamed")
	require.ErrorContains(t, err, `folder "skills-renamed" not found`)
	skills, _, err := DiscoverSkillsInTarball(bytes.NewReader(repo.Bytes()), "skills")
	require.NoError(t, err)
	assert.Len(t, skills, 1)
}
