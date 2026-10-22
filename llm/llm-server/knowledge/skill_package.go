package knowledge

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Agent Skills folders (https://agentskills.io/specification): a SKILL.md with
// YAML frontmatter (name, description, optional license, compatibility,
// metadata, allowed-tools) and any other files. Parsing follows the format's
// client guide: forgiving — a cosmetic problem is a warning and the skill still
// loads; only a missing description or an unreadable header skips it.

const (
	// SkillFileMaxBytes caps one file of a skill; larger files are skipped.
	SkillFileMaxBytes = 1 << 20
	// SkillMaxBytes caps all files of one skill, SKILL.md included.
	SkillMaxBytes = 10 << 20
	// SkillMarkdown is the file every Agent Skills folder has.
	SkillMarkdown = "SKILL.md"
)

var skillSpecNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// SkillRequirement is something a skill needs to run that its text names.
type SkillRequirement struct {
	// Kind is cli, mcp, runtime, python (a package a script imports) or note
	// (free text from `compatibility`).
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Provided is false when Nudgebee's workspace has no such tool. MCP
	// servers and notes are never assumed provided.
	Provided bool `json:"provided"`
}

// SkillFileInfo describes one file of a skill folder.
type SkillFileInfo struct {
	Path string `json:"path"`
	Size int    `json:"size"`
	// Skipped says why the file was not stored ("" when it was).
	Skipped string `json:"skipped,omitempty"`
}

// SkillMeta is what llm_knowledgebases.skill_meta holds for a synced skill.
type SkillMeta struct {
	SourcePath    string             `json:"source_path"`
	Commit        string             `json:"commit,omitempty"`
	License       string             `json:"license,omitempty"`
	Compatibility string             `json:"compatibility,omitempty"`
	Metadata      map[string]string  `json:"metadata,omitempty"`
	AllowedTools  string             `json:"allowed_tools,omitempty"`
	Requirements  []SkillRequirement `json:"requirements,omitempty"`
	Warnings      []string           `json:"warnings,omitempty"`
	Files         []SkillFileInfo    `json:"files,omitempty"`
}

// SkillPackage is one parsed Agent Skills folder.
type SkillPackage struct {
	Name        string
	Description string
	// Body is SKILL.md without its frontmatter: what an agent reads first.
	Body string
	// Files are the other stored text files, path relative to the skill root.
	Files map[string]string
	Meta  SkillMeta
}

// ParseSkillFolder parses one skill folder. dir is the folder's path inside
// its source (used for the name check and as its identity); files maps paths
// relative to the folder to their bytes and must include SKILL.md; tooLarge
// lists files over the per-file cap that were not read (path -> size). A
// returned error means the skill is skipped; warnings are on the package.
func ParseSkillFolder(dir string, files map[string][]byte, tooLarge map[string]int64) (*SkillPackage, error) {
	raw, ok := files[SkillMarkdown]
	if !ok {
		return nil, fmt.Errorf("no %s", SkillMarkdown)
	}
	front, body, err := splitFrontmatter(string(raw))
	if err != nil {
		return nil, err
	}
	fields, err := parseFrontmatter(front)
	if err != nil {
		return nil, fmt.Errorf("unreadable %s header: %w", SkillMarkdown, err)
	}

	pkg := &SkillPackage{Body: strings.TrimSpace(body), Files: map[string]string{}}
	pkg.Meta.SourcePath = dir
	warn := func(format string, args ...any) {
		pkg.Meta.Warnings = append(pkg.Meta.Warnings, fmt.Sprintf(format, args...))
	}

	pkg.Description = strings.TrimSpace(stringField(fields, "description"))
	if pkg.Description == "" {
		return nil, fmt.Errorf("%s has no description", SkillMarkdown)
	}
	if n := utf8.RuneCountInString(pkg.Description); n > 1024 {
		warn("description is %d characters; the format allows 1024", n)
	}

	folderName := path.Base(dir)
	pkg.Name = strings.TrimSpace(stringField(fields, "name"))
	switch {
	case pkg.Name == "":
		pkg.Name = folderName
		warn("no name in %s; using the folder name %q", SkillMarkdown, folderName)
	case pkg.Name != folderName && dir != "" && dir != ".":
		warn("name %q does not match its folder %q", pkg.Name, folderName)
	}
	if strings.ContainsAny(pkg.Name, "/,") {
		// load_skills reads "<skill>/<file>" and splits lists on commas.
		return nil, fmt.Errorf("name %q contains / or ,", pkg.Name)
	}
	if !skillSpecNameRe.MatchString(pkg.Name) || len(pkg.Name) > 64 {
		warn("name %q is not 1-64 lowercase letters, digits and single hyphens", pkg.Name)
	}

	pkg.Meta.License = stringField(fields, "license")
	pkg.Meta.Compatibility = stringField(fields, "compatibility")
	pkg.Meta.AllowedTools = stringField(fields, "allowed-tools")
	if m, ok := fields["metadata"].(map[string]any); ok {
		pkg.Meta.Metadata = map[string]string{}
		for k, v := range m {
			pkg.Meta.Metadata[k] = fmt.Sprint(v)
		}
	}

	total := len(raw)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	for p := range tooLarge {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var scripts []string
	for _, p := range paths {
		if p == SkillMarkdown {
			continue
		}
		content := files[p]
		info := SkillFileInfo{Path: p, Size: len(content)}
		if size, big := tooLarge[p]; big {
			info.Size = int(size)
		}
		switch {
		case tooLarge[p] > 0 || len(content) > SkillFileMaxBytes:
			info.Skipped = fmt.Sprintf("larger than %d MB", SkillFileMaxBytes>>20)
		case !safeSkillPath(p):
			info.Skipped = "unsafe path"
		case !isText(content):
			info.Skipped = "not a text file"
		case total+len(content) > SkillMaxBytes:
			info.Skipped = fmt.Sprintf("skill over %d MB", SkillMaxBytes>>20)
		default:
			total += len(content)
			pkg.Files[p] = string(content)
			if strings.HasPrefix(p, "scripts/") {
				scripts = append(scripts, p)
			}
		}
		if info.Skipped != "" {
			warn("%s skipped: %s", p, info.Skipped)
		}
		pkg.Meta.Files = append(pkg.Meta.Files, info)
	}

	pkg.Meta.Requirements = detectSkillRequirements(pkg.Body, pkg.Meta.Compatibility, pkg.Meta.AllowedTools, scripts, pkg.Files)
	for _, r := range pkg.Meta.Requirements {
		if !r.Provided && r.Kind != "note" {
			kind := r.Kind
			if kind == "python" {
				kind = "Python package"
			}
			warn("needs %s %q, which Nudgebee does not provide", kind, r.Name)
		}
	}
	return pkg, nil
}

// splitFrontmatter returns the YAML between the opening and closing "---"
// lines and the text after it.
func splitFrontmatter(text string) (string, string, error) {
	text = strings.TrimPrefix(text, "\ufeff")
	lines := strings.SplitAfter(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", fmt.Errorf("%s does not start with a --- header", SkillMarkdown)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], ""), strings.Join(lines[i+1:], ""), nil
		}
	}
	return "", "", fmt.Errorf("%s header has no closing ---", SkillMarkdown)
}

// parseFrontmatter reads the header as YAML. Headers written for other clients
// often have unquoted values containing ": " (invalid YAML that their parsers
// accept), so a failed parse is retried with such top-level values quoted.
func parseFrontmatter(front string) (map[string]any, error) {
	fields := map[string]any{}
	err := yaml.Unmarshal([]byte(front), &fields)
	if err == nil {
		return fields, nil
	}
	fields = map[string]any{}
	if retryErr := yaml.Unmarshal([]byte(quoteTopLevelValues(front)), &fields); retryErr != nil {
		return nil, err
	}
	return fields, nil
}

var topLevelKeyRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*):[ \t]+(.+)$`)

func quoteTopLevelValues(front string) string {
	lines := strings.Split(front, "\n")
	for i, l := range lines {
		m := topLevelKeyRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[2])
		if strings.HasPrefix(v, `"`) || strings.HasPrefix(v, `'`) || strings.HasPrefix(v, "|") || strings.HasPrefix(v, ">") {
			continue
		}
		lines[i] = m[1] + ": " + fmt.Sprintf("%q", v)
	}
	return strings.Join(lines, "\n")
}

func stringField(fields map[string]any, key string) string {
	switch v := fields[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// safeSkillPath accepts a clean relative path inside the skill folder.
func safeSkillPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return false
	}
	if path.Clean(p) != p {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." {
			return false
		}
	}
	return true
}

func isText(b []byte) bool {
	return utf8.Valid(b) && !bytes.Contains(b, []byte{0})
}

// Tools a skill's text commonly names, and whether Nudgebee's workspace image
// (llm/code-analysis/Dockerfile) can run them: installed there, or a shim that
// runs the command server-side with the account's credentials (kubectl, helm,
// psql, argocd… the same commands kubectl_execute / helm_execute run). An
// explicit table, not a guess: add a row when the image changes.
// TestKnownSkillCLIsMatchWorkspaceShims fails when a shimmed one is marked missing.
var knownSkillCLIs = map[string]bool{
	"kubectl": true, "aws": true, "gcloud": true, "gsutil": true, "bq": true, "az": true,
	"gh": true, "glab": true, "git": true, "jq": true, "curl": true, "nbctl": true,
	"helm": true, "argocd": true, "psql": true, "mysql": true, "redis-cli": true,
	"clickhouse-client": true, "sqlcmd": true, "sqlplus": true,
	"terraform": false, "packer": false, "pup": false, "k6": false,
	"logcli": false, "profilecli": false, "promtool": false, "mimirtool": false,
	"docker": false, "datadog-ci": false, "vault": false,
}

var scriptRuntimes = map[string]string{
	".py": "python", ".sh": "shell", ".bash": "shell", ".js": "node", ".mjs": "node", ".ts": "node",
}

// Runtimes the workspace image has.
var providedRuntimes = map[string]bool{"python": true, "shell": true, "node": true}

// knownPythonPackages maps a module a skill's script commonly imports to its
// pip package and whether the workspace image's python3 has it (checked with
// importlib in llm/code-analysis's image; it has few). Explicit, like
// knownSkillCLIs: the standard library and the skill's own modules are not
// listed, so they are never flagged; add a row when the image changes.
var knownPythonPackages = map[string]struct {
	Package  string
	Provided bool
}{
	"yaml": {"pyyaml", false}, "requests": {"requests", false}, "jinja2": {"jinja2", false},
	"numpy": {"numpy", false}, "pandas": {"pandas", false}, "matplotlib": {"matplotlib", false},
	"boto3": {"boto3", false}, "botocore": {"botocore", false}, "kubernetes": {"kubernetes", false},
	"httpx": {"httpx", false}, "pydantic": {"pydantic", false}, "click": {"click", false},
	"rich": {"rich", false}, "anthropic": {"anthropic", false}, "openai": {"openai", false},
	"bs4": {"beautifulsoup4", false}, "lxml": {"lxml", false}, "pytz": {"pytz", false},
	"markdown": {"markdown", false}, "tabulate": {"tabulate", false}, "psycopg2": {"psycopg2", false},
	"pymysql": {"pymysql", false}, "redis": {"redis", false}, "jsonschema": {"jsonschema", false},
	"toml": {"toml", false}, "tqdm": {"tqdm", false}, "dotenv": {"python-dotenv", false},
	"prometheus_client": {"prometheus-client", false},
	"dateutil":          {"python-dateutil", true}, "urllib3": {"urllib3", true}, "certifi": {"certifi", true},
	"cryptography": {"cryptography", true}, "packaging": {"packaging", true},
}

// pythonImportRe matches "import a, b" and "from a.b import c" lines.
var pythonImportRe = regexp.MustCompile(`(?m)^\s*(?:from\s+([A-Za-z_]\w*)[\w.]*\s+import\b|import\s+([A-Za-z_][\w.]*(?:\s*,\s*[A-Za-z_][\w.]*)*))`)

// MCP servers a skill may name, as "<vendor> MCP", "mcp-<vendor>",
// "<vendor>-mcp" or a "mcp__<vendor>__" tool prefix.
var knownMCPVendors = []string{"grafana", "datadog", "github", "gitlab", "slack", "pagerduty", "sentry", "elastic", "atlassian", "kubernetes", "aws", "azure", "honeycomb", "newrelic"}

var (
	inlineCodeRe  = regexp.MustCompile("`([^`\n]+)`")
	fencedCodeRe  = regexp.MustCompile("(?s)```[^\n]*\n(.*?)```")
	allowedBashRe = regexp.MustCompile(`Bash\(([A-Za-z0-9_.-]+)`)
)

// detectSkillRequirements lists likely needs: the `compatibility` note, CLIs
// from allowed-tools and from commands in the skill's code blocks, script
// runtimes, packages the Python scripts import, and named MCP servers.
func detectSkillRequirements(body, compatibility, allowedTools string, scripts []string, files map[string]string) []SkillRequirement {
	seen := map[string]bool{}
	var out []SkillRequirement
	add := func(kind, name string, provided bool) {
		key := kind + ":" + name
		if name == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, SkillRequirement{Kind: kind, Name: name, Provided: provided})
	}

	if c := strings.TrimSpace(compatibility); c != "" {
		add("note", c, false)
	}
	for _, m := range allowedBashRe.FindAllStringSubmatch(allowedTools, -1) {
		if provided, known := knownSkillCLIs[m[1]]; known {
			add("cli", m[1], provided)
		}
	}
	var code []string
	for _, m := range fencedCodeRe.FindAllStringSubmatch(body, -1) {
		code = append(code, strings.Split(m[1], "\n")...)
	}
	for _, m := range inlineCodeRe.FindAllStringSubmatch(body, -1) {
		code = append(code, m[1])
	}
	for _, line := range code {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "$ "))
		if len(fields) == 0 {
			continue
		}
		if provided, known := knownSkillCLIs[fields[0]]; known {
			add("cli", fields[0], provided)
		}
	}
	for _, s := range scripts {
		if rt, ok := scriptRuntimes[path.Ext(s)]; ok {
			add("runtime", rt, providedRuntimes[rt])
		}
	}
	for _, s := range scripts {
		if path.Ext(s) != ".py" {
			continue
		}
		for _, m := range pythonImportRe.FindAllStringSubmatch(files[s], -1) {
			modules := []string{m[1]}
			if m[1] == "" {
				modules = strings.Split(m[2], ",")
			}
			for _, mod := range modules {
				mod, _, _ = strings.Cut(strings.TrimSpace(mod), ".")
				if pkg, known := knownPythonPackages[mod]; known {
					add("python", pkg.Package, pkg.Provided)
				}
			}
		}
	}
	lower := strings.ToLower(body)
	for _, v := range knownMCPVendors {
		if strings.Contains(lower, v+" mcp") || strings.Contains(lower, "mcp-"+v) ||
			strings.Contains(lower, v+"-mcp") || strings.Contains(lower, "mcp__"+v+"__") {
			add("mcp", v, false)
		}
	}
	return out
}
