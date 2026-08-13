package planners

import (
	"fmt"
	"strings"
)

// Grounding enforces that an explore-mode answer cites code the agent actually
// read. A search hit (grep/rg) only locates a candidate — it is not evidence: a
// broad pattern can match an unrelated substring of a longer identifier or a
// test fixture, and citing such a hit without opening the file yields
// confident-but-wrong answers. The gate rejects a submit_analysis whose citations
// all point at files that were never opened with file_view, so the agent either
// opens the file and cites real lines, or honestly abstains.

func normPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	return strings.TrimPrefix(p, "/")
}

// recordFileRead marks a file as opened via file_view during this run.
func (p *ReActPlanner) recordFileRead(path string) {
	path = normPath(path)
	if path == "" {
		return
	}
	p.mu.Lock()
	if p.readFiles == nil {
		p.readFiles = make(map[string]bool)
	}
	p.readFiles[path] = true
	p.mu.Unlock()
}

// wasFileRead reports whether a cited path matches a file opened with file_view.
// Matching is suffix-based so relative citations line up with absolute or
// differently-rooted read paths without false rejections.
func (p *ReActPlanner) wasFileRead(cited string) bool {
	cited = normPath(cited)
	if cited == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for r := range p.readFiles {
		if r == cited || strings.HasSuffix(r, "/"+cited) || strings.HasSuffix(cited, "/"+r) {
			return true
		}
	}
	return false
}

// citationGrounding inspects a submit_analysis input's structured citations and
// reports how many point at files that were actually opened with file_view.
func (p *ReActPlanner) citationGrounding(input map[string]any) (total, grounded int, ungrounded []string) {
	cits, ok := input["citations"].([]any)
	if !ok {
		return 0, 0, nil
	}
	for _, c := range cits {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		fp, _ := m["file_path"].(string)
		if strings.TrimSpace(fp) == "" {
			continue
		}
		total++
		if p.wasFileRead(fp) {
			grounded++
		} else {
			ungrounded = append(ungrounded, fp)
		}
	}
	return total, grounded, ungrounded
}

// checkSubmitGrounding returns (reject, message). It rejects only when EVERY
// citation is ungrounded — the hallucination signature — so a submit with mixed
// or fully-grounded evidence always passes. A submit with no structured
// citations is out of scope here (non-explore mode / handled by the tool's own
// citation-required contract) and passes.
func (p *ReActPlanner) checkSubmitGrounding(input map[string]any) (bool, string) {
	total, grounded, ungrounded := p.citationGrounding(input)
	if total == 0 || grounded > 0 {
		return false, ""
	}
	return true, fmt.Sprintf(
		"UNGROUNDED SUBMISSION: your citations reference %d file(s) you never opened with file_view (%s). "+
			"A grep/search hit is a lead, not evidence. Open the cited file with file_view and cite the lines you actually read. "+
			"If you cannot find code that supports the claim, do NOT guess — submit an honest answer stating the evidence is insufficient and what you were unable to confirm.",
		len(ungrounded), strings.Join(ungrounded, ", "))
}
