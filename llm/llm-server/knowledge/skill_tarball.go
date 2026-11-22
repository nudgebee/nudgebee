package knowledge

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// Bounds for one source download, so a huge or hostile repo can't exhaust the
// server: the whole archive, the number of entries, and how deep a skill
// folder may sit under the source folder.
const (
	skillSourceMaxBytes   = 100 << 20
	skillSourceMaxEntries = 20000
	skillSourceMaxDepth   = 6
)

// skillSourceMaxUnpacked bounds the decompressed archive, so a small download
// that inflates hugely can't tie up the server (a variable for tests).
var skillSourceMaxUnpacked int64 = 512 << 20

// SkippedSkill is a SKILL.md folder that could not be read.
type SkippedSkill struct {
	SourcePath string `json:"source_path"`
	Reason     string `json:"reason"`
}

// DiscoverSkillsInTarball reads a gzipped repo tarball, as it streams in, (GitHub's layout: one
// top-level directory) and returns every Agent Skills folder under folder:
// each directory holding a SKILL.md, with the files beneath it. A file belongs
// to its nearest skill folder. Symlinks and other non-regular entries are
// ignored. Skill paths are relative to the repo root.
func DiscoverSkillsInTarball(r io.Reader, folder string) ([]*SkillPackage, []SkippedSkill, error) {
	download := &capReader{r: r, left: skillSourceMaxBytes}
	gz, err := gzip.NewReader(download)
	if err != nil {
		if download.over {
			return nil, nil, errSkillSourceTooLarge
		}
		return nil, nil, fmt.Errorf("read archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	unpacked := &io.LimitedReader{R: gz, N: skillSourceMaxUnpacked}
	folder = strings.Trim(path.Clean("/"+folder), "/")

	type entry struct {
		path string
		data []byte
		// size of a file over the per-file cap, which is never read.
		tooLarge int64
	}
	var entries []entry
	var total int64
	// files counts the regular files under folder: the entry limit is about
	// what a sync reads, so a large monorepo with a small skills folder syncs.
	files := 0
	tr := tar.NewReader(unpacked)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			// The limit returns a clean EOF: a cut on a file boundary would
			// otherwise read as a complete repo and archive every skill after it.
			if unpacked.N <= 0 {
				return nil, nil, fmt.Errorf("repository archive is larger than %d MB unpacked", skillSourceMaxUnpacked>>20)
			}
			break
		}
		if err != nil {
			if download.over {
				return nil, nil, errSkillSourceTooLarge
			}
			if unpacked.N <= 0 {
				return nil, nil, fmt.Errorf("repository archive is larger than %d MB unpacked", skillSourceMaxUnpacked>>20)
			}
			return nil, nil, fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Drop GitHub's "<owner>-<repo>-<sha>/" prefix.
		p := hdr.Name
		if i := strings.Index(p, "/"); i >= 0 {
			p = p[i+1:]
		}
		if p == "" || !safeSkillPath(p) {
			continue
		}
		if folder != "" && p != folder && !strings.HasPrefix(p, folder+"/") {
			continue
		}
		if files++; files > skillSourceMaxEntries {
			return nil, nil, fmt.Errorf("repository folder has more than %d files", skillSourceMaxEntries)
		}
		if hdr.Size > SkillFileMaxBytes {
			// Keep the name so the skill reports it; never read it.
			entries = append(entries, entry{path: p, tooLarge: hdr.Size})
			continue
		}
		total += hdr.Size
		if total > skillSourceMaxBytes {
			return nil, nil, fmt.Errorf("repository folder is larger than %d MB", skillSourceMaxBytes>>20)
		}
		b, err := io.ReadAll(io.LimitReader(tr, SkillFileMaxBytes+1))
		if err != nil {
			if download.over {
				return nil, nil, errSkillSourceTooLarge
			}
			return nil, nil, fmt.Errorf("read %s: %w", p, err)
		}
		entries = append(entries, entry{path: p, data: b})
	}

	// A folder renamed or removed upstream is an error, not a source with no
	// skills: that would archive every skill it had.
	if folder != "" && files == 0 {
		return nil, nil, fmt.Errorf("folder %q not found in the repository", folder)
	}

	// Skill folders: directories with a SKILL.md, not too deep under folder.
	skillDirs := map[string]bool{}
	for _, e := range entries {
		if path.Base(e.path) != SkillMarkdown {
			continue
		}
		dir := path.Dir(e.path)
		if dir == "." {
			dir = ""
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(dir, folder), "/")
		if rel != "" && strings.Count(rel, "/")+1 > skillSourceMaxDepth {
			continue
		}
		skillDirs[dir] = true
	}

	grouped := map[string]map[string][]byte{}
	tooLarge := map[string]map[string]int64{}
	for _, e := range entries {
		owner, ok := nearestSkillDir(e.path, skillDirs)
		if !ok {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(e.path, owner), "/")
		if e.tooLarge > 0 {
			if tooLarge[owner] == nil {
				tooLarge[owner] = map[string]int64{}
			}
			tooLarge[owner][rel] = e.tooLarge
			continue
		}
		if grouped[owner] == nil {
			grouped[owner] = map[string][]byte{}
		}
		grouped[owner][rel] = e.data
	}

	dirs := make([]string, 0, len(grouped))
	for d := range grouped {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var skills []*SkillPackage
	var skipped []SkippedSkill
	for _, d := range dirs {
		pkg, err := ParseSkillFolder(d, grouped[d], tooLarge[d])
		if err != nil {
			skipped = append(skipped, SkippedSkill{SourcePath: d, Reason: err.Error()})
			continue
		}
		skills = append(skills, pkg)
	}
	return skills, skipped, nil
}

var errSkillSourceTooLarge = fmt.Errorf("repository is larger than %d MB", skillSourceMaxBytes>>20)

// capReader reads the download as it streams in and fails once it passes
// left bytes, so an oversized repository stops with a clear error instead of
// a truncated archive.
type capReader struct {
	r    io.Reader
	left int64
	over bool
}

func (c *capReader) Read(p []byte) (int, error) {
	// Read up to one byte past the cap: a download of exactly left bytes then
	// ends with EOF, and only a byte beyond it is an error.
	if int64(len(p)) > c.left {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	if int64(n) > c.left {
		c.over = true
		return int(c.left), errSkillSourceTooLarge
	}
	c.left -= int64(n)
	return n, err
}

// nearestSkillDir returns the deepest skill folder containing p.
func nearestSkillDir(p string, skillDirs map[string]bool) (string, bool) {
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		if dir == "." {
			dir = ""
		}
		if skillDirs[dir] {
			return dir, true
		}
		if dir == "" {
			return "", false
		}
	}
}
