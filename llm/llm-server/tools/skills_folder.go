package tools

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/knowledge"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// skillFolderMarker sits in a copied skill folder and holds the content
// version it was written from, so a later load rewrites only when the skill
// changed or the workspace lost the folder.
const skillFolderMarker = ".skill-version"

var (
	getSkillFolderFn       = knowledge.GetSkillFolder
	newSkillWorkspace      = func() workspace.WorkspaceManager { return workspace.NewWorkspaceManagerWithTimeout(90 * time.Second) }
	skillScriptsRecordedFn = func(accountId, conversationId string) bool {
		return knowledge.ConversationHasSkillScripts(accountId, conversationId)
	}
)

// splitSkillFolderName recognises "<skill>/": the request to copy a synced
// skill's folder into the workspace.
func splitSkillFolderName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if strings.Contains(name, ",") || !strings.HasSuffix(name, "/") {
		return "", false
	}
	skill := strings.TrimSpace(strings.TrimSuffix(name, "/"))
	if skill == "" || strings.Contains(skill, "/") || strings.HasPrefix(strings.ToLower(skill), "knowledge:") {
		return "", false
	}
	return skill, true
}

// callSkillFolder copies a synced skill's folder (SKILL.md and its files) into
// the conversation's workspace at skills/<name>/, so its scripts can run
// there with shell_execute. From then on any code run in the conversation
// asks for approval (see authorizeToolRequest).
func (m LoadSkillsTool) callSkillFolder(ctx core.NbToolContext, skill string) (core.NBToolResponse, error) {
	if !config.Config.LlmServerKnowledgeWorkspaceEnabled {
		return knowledgeFailure(fmt.Errorf("running a skill's scripts needs the workspace, which is turned off on this server"))
	}
	if ctx.AccountId == "" || ctx.ConversationId == "" {
		return knowledgeFailure(fmt.Errorf("a skill folder can only be copied inside a conversation"))
	}
	folder, err := getSkillFolderFn(ctx.Ctx, ctx.AccountId, skill)
	if err != nil {
		return knowledgeFailure(err)
	}
	if folder == nil {
		return knowledgeFailure(fmt.Errorf("no skill synced from a repository is named %q; only those have a folder to copy", skill))
	}
	name := skillDirName(folder.SkillName)
	if !skillDirNameRe.MatchString(name) {
		// Synced names are only checked for "/" and ","; "..", "." or a name
		// with other characters would put the files outside skills/<name>/.
		return knowledgeFailure(fmt.Errorf("skill name %q can't be used as a folder name; rename the skill in its repository", folder.SkillName))
	}
	dir := "skills/" + name
	// This call's own tool-call record is what makes the conversation ask
	// before running code (see knowledge.ConversationHasSkillScripts). It is
	// written before the tool runs; if it can't be found, copy nothing, so
	// scripts never sit in a workspace whose conversation doesn't ask.
	if !skillScriptsRecordedFn(ctx.AccountId, ctx.ConversationId) {
		return knowledgeFailure(fmt.Errorf("this request isn't recorded in the conversation, so the skill's scripts can't be put behind approval; nothing was copied"))
	}
	wm := newSkillWorkspace()
	written, err := writeSkillFolder(ctx, wm, dir, folder)
	if err != nil {
		return knowledgeFailure(fmt.Errorf("copy skill %s into the workspace: %w", folder.SkillName, err))
	}
	common.MetricsToolOperationsTotal(core.ToolImplTypeBuiltin, m.Name(), "success", ctx.AccountId)

	paths := make([]string, 0, len(folder.Files))
	for p := range folder.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	state := "Copied"
	if !written {
		state = "Already up to date:"
	}
	fmt.Fprintf(&b, "%s skill %s at %s/ in the workspace (SKILL.md and %d files):\n", state, folder.SkillName, dir, len(paths))
	for _, p := range paths {
		fmt.Fprintf(&b, "- %s/%s\n", dir, p)
	}
	fmt.Fprintf(&b, "Read its files with cat, sed -n, head or grep: reads need no approval, so don't use python3 or bash just to read one. "+
		"Run its scripts with shell_execute using work_dir %q, for example `python3 scripts/<file>.py` or `bash scripts/<file>.sh`; "+
		"the user is asked to approve every command that runs code in this conversation, each time. "+
		"Only what the workspace already has is available: install nothing, and don't borrow packages from elsewhere (PYTHONPATH). "+
		"If a script fails for a missing package, say which one instead of working around it.", dir)
	return core.NBToolResponse{
		Status:     core.NBToolResponseStatusSuccess,
		Type:       core.NBToolResponseTypeText,
		Data:       b.String(),
		References: []core.NBToolResponseReference{{Text: folder.SkillName, Url: folder.SkillID, Type: "skill"}},
	}, nil
}

// writeSkillFolder writes the folder unless its marker already holds this
// version. The marker goes last, so an interrupted copy is redone next time.
func writeSkillFolder(ctx core.NbToolContext, wm workspace.WorkspaceManager, dir string, folder *knowledge.SkillFolder) (bool, error) {
	version := folder.ContentSHA
	if version != "" {
		if got, err := wm.ReadFile(ctx.Ctx, ctx.AccountId, ctx.ConversationId, path.Join(dir, skillFolderMarker)); err == nil && strings.TrimSpace(string(got)) == version {
			return false, nil
		}
	}
	// Same budget as a large skill written to knowledge/<hash>.txt: at most
	// knowledgeSaveSlots copies into workspaces at once per server.
	select {
	case knowledgeSaveSlots <- struct{}{}:
		defer func() { <-knowledgeSaveSlots }()
	case <-ctx.Ctx.GetContext().Done():
		return false, ctx.Ctx.GetContext().Err()
	}
	save := func(p, content string) error {
		_, err := wm.CallAPIOrLazyCreate(ctx.Ctx, ctx.AccountId, "POST", "/api/v1/files/save", nil, map[string]string{
			"path": p, "conversation_id": ctx.ConversationId, "content": content,
		})
		return err
	}
	if err := save(path.Join(dir, knowledge.SkillMarkdown), folder.SkillMD); err != nil {
		return false, err
	}
	for p, content := range folder.Files {
		if p == knowledge.SkillMarkdown || p == skillFolderMarker {
			continue // written from the stored skill and by us
		}
		if err := save(path.Join(dir, p), content); err != nil {
			return false, fmt.Errorf("%s: %w", p, err)
		}
	}
	if err := save(path.Join(dir, skillFolderMarker), version); err != nil {
		return false, err
	}
	return true, nil
}

// skillDirNameRe is what a skill's folder name may be: a letter or digit,
// then letters, digits, dots, hyphens or underscores.
var skillDirNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// skillDirName is the folder name for a skill: its name, lower-cased, spaces
// as hyphens (skill names are already free of "/" and ",").
func skillDirName(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
}
