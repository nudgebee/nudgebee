package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

const knowledgeReadBytes = 8192

// The existing JSON save API buffers each body. Bound concurrent materialization.
var knowledgeSaveSlots = make(chan struct{}, 2)

func knowledgeFailure(err error) (core.NBToolResponse, error) {
	return core.NBToolResponse{Status: core.NBToolResponseStatusError, Type: core.NBToolResponseTypeText, Data: fmt.Sprintf("Knowledge read failed: %s. No complete document was loaded.", common.TruncateHead(err.Error(), 1024))}, nil
}

func (m LoadSkillsTool) callWorkspaceKnowledge(ctx core.NbToolContext, input core.NBToolCallRequest, name string) (core.NBToolResponse, error) {
	names := m.parseSkillNames(name)
	if len(names) != 1 {
		return knowledgeFailure(fmt.Errorf("select exactly one skill name or candidate ID"))
	}
	name = names[0]
	keyword, _ := input.Arguments["keyword"].(string)
	if len(keyword) > 256 {
		return knowledgeFailure(fmt.Errorf("keyword exceeds 256 bytes"))
	}
	start := 1
	if raw, ok := input.Arguments["start_line"]; ok {
		value, err := strconv.Atoi(fmt.Sprint(raw))
		if err != nil || value < 1 {
			return knowledgeFailure(fmt.Errorf("start_line must be a positive integer"))
		}
		start = value
	}
	deadline, cancel := context.WithTimeout(ctx.Ctx.GetContext(), 90*time.Second)
	defer cancel()
	ctx.Ctx = security.NewRequestContext(deadline, ctx.Ctx.GetSecurityContext(), ctx.Ctx.GetLogger(), ctx.Ctx.GetTracer(), ctx.Ctx.GetMeter())
	c, found := core.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, name)
	if strings.HasPrefix(name, "knowledge:") && !found {
		return knowledgeFailure(fmt.Errorf("candidate expired; run search_skills again"))
	}
	if !found || c.KBID != "" {
		selected, kbType, err := core.ResolveKnowledgeSelection(ctx.Ctx, ctx.AccountId, name, c.KBID)
		if err != nil {
			return knowledgeFailure(err)
		}
		if kbType == "integration" {
			docs := core.QueryRAGCollectionReranked(deadline, ctx.UserId, ctx.AccountId, ctx.Query, ragKBModule, selected.Collection, ragSkillTopK, ctx.ConversationId, ctx.MessageId, "", false)
			var scoped core.RAGSearchResults
			for _, doc := range docs {
				if collection, _ := doc.Metadata["collection"].(string); collection == selected.Collection {
					scoped = append(scoped, doc)
				}
			}
			menu := cacheSearchKnowledgeCandidates(ctx, scoped)
			if len(menu) == 0 {
				return knowledgeFailure(fmt.Errorf("no indexed documents found in this knowledge base"))
			}
			return core.NBToolResponse{Status: core.NBToolResponseStatusSuccess, Type: core.NBToolResponseTypeText, Data: "Select an exact indexed document with load_skills:\n" + strings.Join(menu, "\n")}, nil
		}
		if c.FileSHA256 != "" && selected.Version != c.Version {
			return knowledgeFailure(fmt.Errorf("manual knowledge changed; select its name again"))
		}
		selected.Path, selected.FileSHA256, selected.ID = c.Path, c.FileSHA256, c.ID
		c = selected
	}
	if c.ID == "" {
		c.ID = core.NewKnowledgeCandidateID(ctx.AccountId, ctx.ConversationId, ctx.MessageId, c.Collection+":"+c.KBID+":"+c.Version)
	}
	wm := workspace.NewWorkspaceManagerWithTimeout(90 * time.Second)
	if c.Path == "" {
		if err := materializeKnowledge(ctx, wm, &c); err != nil {
			return knowledgeFailure(err)
		}
	} else if c.KBID == "" {
		validation, err := core.OpenKnowledgeDocument(deadline, ctx.AccountId, c, true)
		if err != nil {
			return knowledgeFailure(err)
		}
		_ = validation.Close()
	}
	if err := core.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, c); err != nil {
		return knowledgeFailure(err)
	}
	var reader io.ReadCloser
	if c.Path != "" {
		var err error
		reader, err = wm.ReadFileStream(ctx.Ctx, ctx.AccountId, ctx.ConversationId, c.Path)
		if err != nil {
			return knowledgeFailure(fmt.Errorf("workspace file unavailable; search and select again: %w", err))
		}
	} else {
		reader = io.NopCloser(strings.NewReader(c.Content))
	}
	defer func() { _ = reader.Close() }()
	text, err := readKnowledgeLinesForPurpose(reader, c.FileSHA256, keyword, start, c.Purpose)
	if err != nil {
		return knowledgeFailure(err)
	}
	refs := []core.NBToolResponseReference{{Text: c.Title, Url: c.ReferenceID, Type: "knowledge_base", Description: core.KnowledgePurposeGuidance(c.Purpose)}}
	result := fmt.Sprintf("%s\nTitle: %s\nHandle: %s\nSize: %d bytes. Use load_skills with this handle and keyword or start_line for selective reads.\n%s", core.KnowledgePurposeGuidance(c.Purpose), c.Title, c.ID, c.Bytes, text)
	if c.Path != "" {
		refs = append(refs, core.NBToolResponseReference{Text: c.Title, Url: c.Path, Type: "file", Query: c.ID, Description: core.KnowledgePurposeGuidance(c.Purpose) + " Read through load_skills using " + c.ID})
		result = "Saved knowledge document to workspace: " + c.Path + "\nUse shell_execute (grep/tail) on this exact path when available, or load_skills for bounded reads.\n" + result
	}
	return core.NBToolResponse{Status: core.NBToolResponseStatusSuccess, Type: core.NBToolResponseTypeText, Data: result, References: refs}, nil
}

func materializeKnowledge(ctx core.NbToolContext, wm workspace.WorkspaceManager, c *core.KnowledgeCandidate) error {
	select {
	case knowledgeSaveSlots <- struct{}{}:
		defer func() { <-knowledgeSaveSlots }()
	case <-ctx.Ctx.GetContext().Done():
		return ctx.Ctx.GetContext().Err()
	}
	if c.Bytes > core.MaxKnowledgeDocumentBytes {
		return fmt.Errorf("document exceeds the 32 MiB transfer limit")
	}
	if ctx.AccountId == "" || ctx.ConversationId == "" {
		return fmt.Errorf("account and conversation are required")
	}
	temp, err := os.CreateTemp("", "knowledge-*")
	if err != nil {
		return fmt.Errorf("create temporary knowledge file: %w", err)
	}
	defer func() { _ = temp.Close(); _ = os.Remove(temp.Name()) }()
	digest := sha256.New()
	out := io.MultiWriter(temp, digest)
	if c.KBID != "" {
		err = core.WriteManualKnowledge(ctx.Ctx, ctx.AccountId, *c, out)
	} else {
		var source io.ReadCloser
		source, err = core.OpenKnowledgeDocument(ctx.Ctx.GetContext(), ctx.AccountId, *c, false)
		if err == nil {
			var n int64
			n, err = io.Copy(out, io.LimitReader(source, core.MaxKnowledgeDocumentBytes+1))
			_ = source.Close()
			if n > core.MaxKnowledgeDocumentBytes {
				err = fmt.Errorf("document exceeds transfer limit")
			}
			if err == nil && n != c.Bytes {
				err = fmt.Errorf("incomplete indexed document")
			}
		}
	}
	if err != nil {
		return fmt.Errorf("materialize knowledge: %w", err)
	}
	c.FileSHA256 = hex.EncodeToString(digest.Sum(nil))
	if c.KBID == "" && c.FileSHA256 != c.Version {
		return fmt.Errorf("indexed document content version mismatch")
	}
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind knowledge file: %w", err)
	}
	if c.Bytes <= core.KnowledgeExcerptBytes {
		data, err := io.ReadAll(io.LimitReader(temp, core.KnowledgeExcerptBytes+1))
		if err != nil {
			return fmt.Errorf("read small knowledge: %w", err)
		}
		c.Content = string(data)
		c.ExcerptOnly = false
		return nil
	}
	identity := sha256.Sum256([]byte(ctx.AccountId + "\x00" + ctx.ConversationId + "\x00" + c.Collection + "\x00" + c.DocumentID + "\x00" + c.FileSHA256))
	path := fmt.Sprintf("knowledge/%x.txt", identity)
	data, err := io.ReadAll(io.LimitReader(temp, core.MaxKnowledgeDocumentBytes+1))
	if err != nil {
		return fmt.Errorf("read knowledge for workspace save: %w", err)
	}
	if int64(len(data)) != c.Bytes {
		return fmt.Errorf("knowledge content size changed")
	}
	_, err = wm.CallAPIOrLazyCreate(ctx.Ctx, ctx.AccountId, "POST", "/api/v1/files/save", nil, map[string]string{"path": path, "conversation_id": ctx.ConversationId, "content": string(data)})
	if err != nil {
		return fmt.Errorf("save knowledge workspace file: %w", err)
	}
	c.Path = path
	c.Content = ""
	return nil
}

// Scan to EOF to verify integrity before releasing excerpts. Memory and output
// are bounded independently of the document, including when the answer is last.
func readKnowledgeLines(source io.Reader, version, keyword string, start int) (string, error) {
	return readKnowledgeLinesForPurpose(source, version, keyword, start, core.KnowledgePurposeReference)
}

func readKnowledgeLinesForPurpose(source io.Reader, version, keyword string, start int, purpose core.KnowledgeContentPurpose) (string, error) {
	digest := sha256.New()
	limited := &io.LimitedReader{R: source, N: core.MaxKnowledgeDocumentBytes + 1}
	reader := bufio.NewReaderSize(io.TeeReader(limited, digest), 32*1024)
	var output strings.Builder
	line, matches := 1, 0
	matcher := regexp.MustCompile("(?i)" + regexp.QuoteMeta(keyword))
	truncated := false
	tail := ""
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			text := string(fragment)
			search := tail + text
			location := matcher.FindStringIndex(search)
			matching := keyword == "" || location != nil
			if line >= start && matching {
				if (matches >= 100 && (purpose != core.KnowledgePurposeProcedure || keyword != "")) || output.Len() >= knowledgeReadBytes-128 {
					truncated = true
				} else {
					// Include a boundary-spanning match without retaining the whole line.
					if keyword != "" {
						if location != nil {
							text = strings.ToValidUTF8(search[max(0, location[0]-120):], "")
						}
					}
					prefix := fmt.Sprintf("%d: ", line)
					available := knowledgeReadBytes - 128 - output.Len() - len(prefix)
					if len(text) > available {
						text = common.TruncateHead(text, available)
						truncated = true
					}
					output.WriteString(prefix)
					output.WriteString(strings.ToValidUTF8(strings.TrimSuffix(text, "\n"), ""))
					output.WriteByte('\n')
					matches++
				}
			}
			if err == bufio.ErrBufferFull {
				tail = string(fragment[max(0, len(fragment)-256):])
			} else {
				tail = ""
				line++
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil && err != bufio.ErrBufferFull {
			return "", fmt.Errorf("knowledge line read failed: %w", err)
		}
	}

	if limited.N == 0 {
		return "", fmt.Errorf("knowledge read exceeds transfer limit")
	}
	if hex.EncodeToString(digest.Sum(nil)) != version {
		return "", fmt.Errorf("workspace document changed or is incomplete; search and select again")
	}
	if output.Len() == 0 {
		return "No matching lines found in this indexed document.", nil
	}
	if truncated {
		fmt.Fprintf(&output, "\n[Read output limited. Refine keyword or use start_line after the last shown line.]\n")
	}
	return output.String(), nil
}
