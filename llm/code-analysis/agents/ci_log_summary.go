package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/internal/gitprovider"
)

// Budget constants for the CI section handed to the planner. Nothing here ever
// cuts a log at a byte offset: lines are dropped whole, blocks are dropped
// whole, and every drop leaves an explicit omission note.
const (
	ciSectionMaxBytes = 28 * 1024 // hard cap for the whole section
	ciBlockMaxBytes   = 5 * 1024  // per failed-check block
	ciMaxBlocks       = 5         // full blocks per section
	ciMaxIndexLines   = 20        // index entries (one per failed check)
	ciMaxP0Lines      = 60        // first-failure block cap (line-aligned)
	ciMaxCites        = 20        // P1: file:line cite lines
	ciMaxMarkers      = 10        // P2: other marker lines
	ciContextLines    = 2         // P3: context lines after the first-failure block
	ciMaxLineBytes    = 2 * 1024  // a longer single line is replaced whole
	ciNoMarkerTail    = 20        // lines shown for a log with no failure marker
	ciMaxNameRunes    = 80
)

// cmdRunner is deliberately small so CI collection can be tested without a CLI.
type cmdRunner interface {
	Run(string, ...string) (string, error)
}

type commandRunner struct{ agent *PRFollowupAgent }

// LogError surfaces lookups whose failure deliberately yields no CI section.
func (r commandRunner) LogError(msg string, err error) {
	r.agent.logger.Log(common.EventStepFailure, msg, map[string]any{"error": err.Error()})
}

// ciErrorLogger is optionally implemented by a cmdRunner.
type ciErrorLogger interface{ LogError(msg string, err error) }

func logCIError(run cmdRunner, msg string, err error) {
	if l, ok := run.(ciErrorLogger); ok {
		l.LogError(msg, err)
	}
}

func (r commandRunner) Run(name string, args ...string) (string, error) {
	return r.agent.runCommandInDir(name, args...)
}

// ciHead pins CI inspection to what the provider reported as the PR/MR head
// before the base merge moved the workspace HEAD.
type ciHead struct {
	SHA string
	// GitLab only: the MR's head_pipeline from the same response that carries
	// the head SHA. Merged-results pipelines run on a temporary merge commit
	// whose sha never equals the MR head, so SHA matching alone would miss them.
	PipelineID     int64
	PipelineStatus string
}

var ciSHAPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// resolveCIHead asks the provider for the current PR/MR head. It returns the
// zero value on failure (and the error, for logging); callers treat that as
// "no CI section". A branch name is never substituted.
func resolveCIHead(run cmdRunner, provider gitprovider.GitProvider, repo, prNumber string) (ciHead, error) {
	if provider == gitprovider.GitProviderGitLab {
		out, err := run.Run("glab", "api", fmt.Sprintf("projects/%s/merge_requests/%s", url.PathEscape(repo), prNumber))
		if err != nil {
			return ciHead{}, err
		}
		var mr struct {
			SHA      string `json:"sha"`
			DiffRefs struct {
				HeadSHA string `json:"head_sha"`
			} `json:"diff_refs"`
			HeadPipe *struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
			} `json:"head_pipeline"`
		}
		if err := json.Unmarshal([]byte(out), &mr); err != nil {
			return ciHead{}, err
		}
		h := ciHead{SHA: mr.DiffRefs.HeadSHA}
		if h.SHA == "" {
			h.SHA = mr.SHA
		}
		if mr.HeadPipe != nil {
			h.PipelineID, h.PipelineStatus = mr.HeadPipe.ID, mr.HeadPipe.Status
		}
		return h, nil
	}
	out, err := run.Run("gh", "pr", "view", prNumber, "--repo", repo, "--json", "headRefOid", "--jq", ".headRefOid")
	if err != nil {
		return ciHead{}, err
	}
	return ciHead{SHA: strings.TrimSpace(out)}, nil
}

type githubCheckRuns struct {
	CheckRuns []githubCheckRun `json:"check_runs"`
}
type githubCheckRun struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Conclusion  string `json:"conclusion"`
	DetailsURL  string `json:"details_url"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	App         struct {
		ID int64 `json:"id"`
	} `json:"app"`
	CheckSuite struct {
		ID int64 `json:"id"`
	} `json:"check_suite"`
}

var (
	jobURL    = regexp.MustCompile(`^https://[^/]+/[^/]+/[^/]+/actions/runs/[0-9]+/job/([0-9]+)`)
	ansi      = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	timestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[^ ]+\s+`)
	// marker lines carry the failure signal; cite lines point at source.
	marker = regexp.MustCompile(`(?i)(##\[error\]|\berror\b|\bfail(?:ed|ure)?\b|\bfatal\b|\bpanic\b|exit code|traceback)`)
	cite   = regexp.MustCompile(`[A-Za-z0-9_./-]+\.[A-Za-z]{1,5}:[0-9]+(?::[0-9]+)?`)
	// gh prints "gh: Gone (HTTP 410)" / "(HTTP 404)"; match the status phrase, not
	// a bare number that could also appear in a job id inside the command line.
	httpGone     = regexp.MustCompile(`HTTP 410\b`)
	httpNotFound = regexp.MustCompile(`HTTP 404\b`)
)

var markerWords = []string{"##[error]", "error", "fail", "fatal", "panic", "exit code", "traceback"}

// isMarker is the marker regex behind a cheap substring prefilter: the
// case-insensitive regex is ~10x slower and most log lines carry no marker.
func isMarker(text string) bool {
	lower := strings.ToLower(text)
	for _, w := range markerWords {
		if strings.Contains(lower, w) {
			return marker.MatchString(text)
		}
	}
	return false
}

func isCite(text string) bool {
	return strings.Contains(text, ":") && strings.Contains(text, ".") && cite.MatchString(text)
}

// collectCIFailures returns text iff it found work for the planner ("" means
// nothing for the planner: no failed check, lookup failure, only logs that are
// not ready yet, or only failures already in handled). The ids are the failing
// runs the text covers, for the caller to record once the planner has seen
// them; handled is that record from earlier runs (see addressedSourceCheckRun).
func collectCIFailures(run cmdRunner, provider gitprovider.GitProvider, repo, prNumber string, head ciHead, handled map[string]bool) (string, []int64) {
	if provider == gitprovider.GitProviderGitLab {
		return collectGitLabCIFailures(run, repo, prNumber, head, handled)
	}
	if !ciSHAPattern.MatchString(head.SHA) {
		return "", nil
	}
	out, err := run.Run("gh", "api", fmt.Sprintf("repos/%s/commits/%s/check-runs", repo, head.SHA), "--paginate")
	if err != nil {
		logCIError(run, "Failed to list CI check runs", err)
		return "", nil
	}
	// --paginate emits one JSON document per page, concatenated.
	var runs []githubCheckRun
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var page githubCheckRuns
		if err := dec.Decode(&page); err != nil {
			logCIError(run, "Failed to decode CI check runs", err)
			return "", nil
		}
		runs = append(runs, page.CheckRuns...)
	}
	// Group by (app, check suite, name): different workflows share generic job
	// names, so a later green run from another suite must not hide a failure,
	// while re-run attempts stay inside their suite and "latest wins".
	groups := map[string]githubCheckRun{}
	for _, check := range runs {
		key := fmt.Sprintf("%d/%d/%s", check.App.ID, check.CheckSuite.ID, check.Name)
		if previous, ok := groups[key]; !ok || checkRunLater(check, previous) {
			groups[key] = check
		}
	}
	var failed []githubCheckRun
	for _, check := range groups {
		// A failing run an earlier followup already looked at is not new work: it
		// has the same id only while nothing has been pushed or re-run since.
		if check.Conclusion == "failure" && !handled[answeredCommentKey(addressedSourceCheckRun, check.ID)] {
			failed = append(failed, check)
		}
	}
	sort.Slice(failed, func(i, j int) bool {
		if failed[i].Name == failed[j].Name {
			return failed[i].ID < failed[j].ID
		}
		return failed[i].Name < failed[j].Name
	})
	if len(failed) == 0 {
		return "", nil
	}
	blocks := make([]ciBlock, 0, len(failed))
	for _, check := range failed {
		b := ciBlock{name: check.Name, checkID: check.ID}
		m := jobURL.FindStringSubmatch(check.DetailsURL)
		if len(m) != 2 {
			b.unavailable = "external CI, logs not fetched"
			blocks = append(blocks, b)
			continue
		}
		b.jobID, _ = strconv.ParseInt(m[1], 10, 64)
		log, err := run.Run("gh", "api", fmt.Sprintf("repos/%s/actions/jobs/%s/logs", repo, m[1]))
		switch {
		case err != nil && httpGone.MatchString(err.Error()):
			b.unavailable = "logs expired"
		case err != nil && httpNotFound.MatchString(err.Error()):
			b.notReady = true
		case err != nil:
			b.unavailable, b.retryable = "log fetch failed", true
		case strings.TrimSpace(log) == "":
			b.unavailable, b.retryable = "empty log output", true
		default:
			b.lines = parseJobLog(log)
		}
		blocks = append(blocks, b)
	}
	section, rendered := renderCIBlocks(repo, blocks)
	if section == "" {
		return "", nil
	}
	// Report a check only if the planner got everything there is to get for it.
	// Left out, so that a later run still looks at them: a check whose logs are
	// not ready yet, one whose logs could not be read this time, and one whose
	// block was dropped to fit the size budget — those are only named in the index.
	var shown []int64
	work := 0
	for _, b := range blocks {
		if b.notReady {
			continue
		}
		if work < rendered && !b.retryable {
			shown = append(shown, b.checkID)
		}
		work++
	}
	return section, shown
}

// checkRunLater orders re-run attempts: completed_at, then started_at, then id.
func checkRunLater(a, b githubCheckRun) bool {
	if ta, tb := parseCITime(a.CompletedAt), parseCITime(b.CompletedAt); !ta.Equal(tb) {
		return ta.After(tb)
	}
	if ta, tb := parseCITime(a.StartedAt), parseCITime(b.StartedAt); !ta.Equal(tb) {
		return ta.After(tb)
	}
	return a.ID > b.ID
}

func parseCITime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

type ciLine struct {
	step string
	text string
}

type ciBlock struct {
	name        string
	checkID     int64
	jobID       int64
	lines       []ciLine
	unavailable string
	// retryable: the logs were unavailable for a reason that may not hold next
	// time (a failed fetch, an empty body), unlike expired or external logs.
	retryable bool
	notReady  bool
}

// parseJobLog understands the `gh api .../actions/jobs/<id>/logs` stream: a BOM,
// an RFC3339 timestamp per line and `##[group]Run ...` step markers. (There is no
// job/step prefix; that is `gh run view` only.) ANSI and timestamps are stripped,
// adjacent identical lines collapse to `line (x N)`.
func parseJobLog(log string) []ciLine {
	log = strings.TrimPrefix(log, "\ufeff")
	var out []ciLine
	step, last := "", ""
	repeats := 0
	flush := func() {
		switch {
		case repeats == 1:
			out = append(out, ciLine{step, last})
		case repeats > 1:
			out = append(out, ciLine{step, fmt.Sprintf("%s (x %d)", last, repeats)})
		}
		repeats = 0
	}
	for _, raw := range strings.Split(log, "\n") {
		line := strings.TrimSpace(stripCINoise(strings.TrimPrefix(raw, "\ufeff")))
		switch {
		case strings.HasPrefix(line, "##[group]"):
			flush()
			step, last = strings.TrimSpace(strings.TrimPrefix(line, "##[group]")), ""
			continue
		case strings.HasPrefix(line, "##[endgroup]"), line == "":
			continue
		}
		if line == last && repeats > 0 {
			repeats++
			continue
		}
		flush()
		last, repeats = line, 1
	}
	flush()
	return out
}

// stripCINoise removes ANSI escapes and the leading timestamp. The regexes only
// run on lines that can contain them; they dominate runtime on large logs.
func stripCINoise(raw string) string {
	if strings.Contains(raw, "\x1b") {
		raw = ansi.ReplaceAllString(raw, "")
	}
	if len(raw) > 11 && raw[4] == '-' && raw[10] == 'T' {
		raw = timestamp.ReplaceAllString(raw, "")
	}
	return raw
}

func renderCISection(repo string, blocks []ciBlock) string {
	section, _ := renderCIBlocks(repo, blocks)
	return section
}

// renderCIBlocks renders the section and reports how many of the work blocks,
// in order, got a full block rather than only an index line.
func renderCIBlocks(repo string, blocks []ciBlock) (string, int) {
	var work []ciBlock // blocks that count as work; not_ready checks do not
	for _, b := range blocks {
		if !b.notReady {
			work = append(work, b)
		}
	}
	if len(work) == 0 {
		return "", 0
	}
	fetchHint := func(n int) string {
		return fmt.Sprintf("%d blocks omitted, fetch with `gh api repos/%s/actions/jobs/<id>/logs`", n, repo)
	}
	rendered := make([]string, len(work))
	for i, b := range work {
		rendered[i] = renderCIBlock(b)
	}
	shown := len(work)
	if shown > ciMaxBlocks {
		shown = ciMaxBlocks
	}
	for {
		out := assembleCISection(blocks, work, rendered[:shown], repo, fetchHint(len(work)-shown))
		if len(out) <= ciSectionMaxBytes || shown <= 1 {
			return out, shown
		}
		shown-- // drop the whole lowest-priority (last) block
	}
}

func assembleCISection(all, work []ciBlock, rendered []string, repo, omittedNote string) string {
	var sb strings.Builder
	sb.WriteString("### Failed CI checks\n\n")
	n := 0
	for _, b := range all {
		if n == ciMaxIndexLines {
			fmt.Fprintf(&sb, "- %d more not listed\n", len(all)-n)
			break
		}
		n++
		status := "see block below"
		switch {
		case b.notReady:
			status = "logs not ready yet"
		case b.unavailable != "":
			status = "UNVERIFIED: " + b.unavailable
		default:
			omitted := true
			for i := range rendered {
				if work[i].name == b.name && work[i].jobID == b.jobID {
					omitted = false
				}
			}
			if omitted {
				status = "block omitted for size"
			}
		}
		fmt.Fprintf(&sb, "- %s%s: %s\n", ciDisplayName(b.name), ciJobSuffix(b.jobID), status)
	}
	for _, r := range rendered {
		sb.WriteString("\n")
		sb.WriteString(r)
	}
	if len(rendered) < len(work) {
		sb.WriteString("\n" + omittedNote + "\n")
	}
	return sb.String()
}

func ciDisplayName(name string) string {
	r := []rune(name)
	if len(r) > ciMaxNameRunes {
		return string(r[:ciMaxNameRunes]) + "..."
	}
	return name
}

func ciJobSuffix(id int64) string {
	if id == 0 {
		return ""
	}
	return fmt.Sprintf(" (job %d)", id)
}

// ciCand is a candidate line with a priority: 0 first-failure block, 1 cited
// lines, 2 other markers, 3 context.
type ciCand struct {
	idx, prio int
}

func renderCIBlock(b ciBlock) string {
	var sb strings.Builder
	head := "#### " + ciDisplayName(b.name) + ciJobSuffix(b.jobID)
	if b.unavailable != "" {
		return head + "\nUNVERIFIED: " + b.unavailable + "\n"
	}
	lines := b.lines

	first := -1
	for i, l := range lines {
		if isMarker(l.text) {
			first = i
			break
		}
	}
	if first < 0 {
		sb.WriteString(head + "\nno failure marker found; last lines of the log:\n")
		// Fill from the end backwards: the last lines are the most informative.
		// Reserve room for the omission note; the final line is always kept.
		const noteRoom = 48
		floor := len(lines) - ciNoMarkerTail
		if floor < 0 {
			floor = 0
		}
		start, used := len(lines), sb.Len()+noteRoom
		for i := len(lines) - 1; i >= floor; i-- {
			cost := len(fmt.Sprintf("%d: %s\n", i+1, ciLineText(i, lines[i])))
			if start != len(lines) && used+cost > ciBlockMaxBytes {
				break
			}
			used += cost
			start = i
		}
		if start > 0 {
			fmt.Fprintf(&sb, "... %d lines omitted ...\n", start)
		}
		for i := start; i < len(lines); i++ {
			fmt.Fprintf(&sb, "%d: %s\n", i+1, ciLineText(i, lines[i]))
		}
		return sb.String()
	}
	if s := lines[first].step; s != "" {
		head += " - step: " + ciDisplayName(s)
	}
	sb.WriteString(head + "\n")

	// P0: first-failure block, from two lines of lead-in to the end of its step.
	stepEnd := first + 1
	for stepEnd < len(lines) && lines[stepEnd].step == lines[first].step {
		stepEnd++
	}
	stepStart := first
	for stepStart > 0 && lines[stepStart-1].step == lines[first].step {
		stepStart--
	}
	p0Start := first - 2
	if p0Start < stepStart {
		p0Start = stepStart
	}
	p0End := stepEnd
	if p0End-p0Start > ciMaxP0Lines {
		p0End = p0Start + ciMaxP0Lines
	}

	// Candidate sets are computed once, in a single pass over the log.
	var cands []ciCand
	for i := p0Start; i < p0End; i++ {
		cands = append(cands, ciCand{i, 0})
	}
	var cites, marks int
	for i, l := range lines {
		if cites >= ciMaxCites && marks >= ciMaxMarkers {
			break
		}
		if i >= p0Start && i < p0End {
			continue
		}
		switch {
		case cites < ciMaxCites && isCite(l.text):
			cands = append(cands, ciCand{i, 1})
			cites++
		case marks < ciMaxMarkers && isMarker(l.text):
			cands = append(cands, ciCand{i, 2})
			marks++
		}
	}
	have := make(map[int]bool, len(cands))
	for _, c := range cands {
		have[c.idx] = true
	}
	for i := p0End; i < p0End+ciContextLines && i < len(lines); i++ {
		if !have[i] {
			cands = append(cands, ciCand{i, 3})
		}
	}
	sort.Slice(cands, func(a, c int) bool { return cands[a].idx < cands[c].idx })

	// body renders candidates up to maxPrio; P0 lines outside [lo, hi) are trimmed.
	body := func(maxPrio, lo, hi int) string {
		var out bytes.Buffer
		prev := -1
		note := func(from, to int) {
			if to > from {
				fmt.Fprintf(&out, "... %d lines omitted ...\n", to-from)
			}
		}
		for _, c := range cands {
			if c.prio > maxPrio || (c.prio == 0 && (c.idx < lo || c.idx >= hi)) {
				continue
			}
			note(prev+1, c.idx)
			fmt.Fprintf(&out, "%d: %s\n", c.idx+1, ciLineText(c.idx, lines[c.idx]))
			prev = c.idx
		}
		note(prev+1, len(lines))
		return out.String()
	}
	// Drop P3, then P2, then P1 in place; then trim P0 whole lines: lead-in
	// first, then the tail. The first-failure line itself is never trimmed
	// (a single line is at most ciMaxLineBytes, so it always fits).
	room := ciBlockMaxBytes - sb.Len()
	lo, hi := p0Start, p0End
	for {
		for prio := 3; prio >= 0; prio-- {
			if s := body(prio, lo, hi); len(s) <= room {
				return sb.String() + s
			}
		}
		switch {
		case lo < first:
			lo++
		case hi > first+1:
			hi--
		default:
			return sb.String() + body(0, lo, hi)
		}
	}
}

// ciLineText replaces an oversized line whole; it is never cut at a byte offset.
func ciLineText(idx int, l ciLine) string {
	if len(l.text) > ciMaxLineBytes {
		return fmt.Sprintf("[line %d: %d bytes omitted]", idx+1, len(l.text))
	}
	return l.text
}

func collectGitLabCIFailures(run cmdRunner, repo, prNumber string, head ciHead, handled map[string]bool) (string, []int64) {
	failed := func(id int64) (string, []int64) {
		if handled[answeredCommentKey(addressedSourceCheckRun, id)] {
			return "", nil
		}
		return fmt.Sprintf("### Failed CI checks\n\npipeline #%d failed, UNVERIFIED: job logs not fetched\n", id), []int64{id}
	}
	// head_pipeline came from the same pre-sync MR response as the head SHA.
	if head.PipelineID != 0 || head.PipelineStatus != "" {
		if head.PipelineStatus == "failed" {
			return failed(head.PipelineID)
		}
		return "", nil
	}
	// head_pipeline null: fall back to pipelines whose sha is the MR head.
	if !ciSHAPattern.MatchString(head.SHA) {
		return "", nil
	}
	out, err := run.Run("glab", "api", fmt.Sprintf("projects/%s/merge_requests/%s/pipelines", url.PathEscape(repo), prNumber))
	if err != nil {
		return "", nil
	}
	var ps []struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
		SHA    string `json:"sha"`
	}
	if json.Unmarshal([]byte(out), &ps) != nil {
		return "", nil
	}
	var latest int64
	status := ""
	for _, p := range ps {
		if p.SHA == head.SHA && p.ID > latest {
			latest, status = p.ID, p.Status
		}
	}
	if status == "failed" {
		return failed(latest)
	}
	return "", nil
}
