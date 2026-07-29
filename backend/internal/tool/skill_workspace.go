package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
)

const (
	defaultSkillReadMaxChars = 5000
	hardSkillReadMaxChars    = 16000
	defaultSkillListChars    = 8000
	hardSkillListChars       = 12000
	maxSkillListFiles        = 16
	defaultSkillListItems    = 12
	hardSkillListItems       = 24
	skillStorageRoot         = filepolicy.SkillRoot
)

type SkillWorkspaceItem struct {
	ID          string
	Name        string
	Description string
	Files       []model.SkillFile
}

type SkillListTool struct {
	skills []SkillWorkspaceItem
}

type SkillReadTool struct {
	skills map[string]SkillWorkspaceItem
}

type SkillSearchTool struct {
	skills []SkillWorkspaceItem
}

type SkillListOutput struct {
	Skills        []SkillListItem `json:"skills"`
	Scope         string          `json:"scope"`
	SkillID       string          `json:"skill_id,omitempty"`
	Truncated     bool            `json:"truncated"`
	OmittedSkills int             `json:"omitted_skills,omitempty"`
	OmittedFiles  int             `json:"omitted_files,omitempty"`
	StartOffset   int             `json:"start_offset"`
	NextOffset    int             `json:"next_offset"`
	HasMore       bool            `json:"has_more"`
	Message       string          `json:"message"`
	Error         string          `json:"error,omitempty"`
}

type SkillListItem struct {
	ID             string                `json:"skill_id"`
	Name           string                `json:"name"`
	Description    string                `json:"description,omitempty"`
	Files          []SkillFileToolOutput `json:"files"`
	FilesTruncated bool                  `json:"files_truncated,omitempty"`
	NextFileOffset int                   `json:"next_file_offset,omitempty"`
}

type SkillFileToolOutput struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
}

type SkillListInput struct {
	SkillID  string `json:"skill_id,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	MaxItems int    `json:"max_items,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

type SkillReadInput struct {
	SkillID  string `json:"skill_id"`
	Path     string `json:"path"`
	Offset   int    `json:"offset,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

type SkillReadOutput struct {
	SkillID     string `json:"skill_id"`
	Path        string `json:"path"`
	Kind        string `json:"kind,omitempty"`
	StartOffset int    `json:"start_offset"`
	NextOffset  int    `json:"next_offset"`
	Truncated   bool   `json:"truncated"`
	Content     string `json:"content,omitempty"`
	Message     string `json:"message"`
	Error       string `json:"error,omitempty"`
}

type SkillSearchInput struct {
	SkillID    string `json:"skill_id,omitempty"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

type SkillSearchOutput struct {
	Query   string             `json:"query"`
	Results []SkillSearchMatch `json:"results"`
	Message string             `json:"message"`
	Error   string             `json:"error,omitempty"`
}

type SkillSearchMatch struct {
	SkillID string `json:"skill_id"`
	Path    string `json:"path"`
	Offset  int    `json:"offset"`
	Snippet string `json:"snippet"`
}

func NewSkillListTool(skills []SkillWorkspaceItem) *SkillListTool {
	return &SkillListTool{skills: normalizeSkillWorkspace(skills)}
}

func NewSkillReadTool(skills []SkillWorkspaceItem) *SkillReadTool {
	normalized := normalizeSkillWorkspace(skills)
	byID := make(map[string]SkillWorkspaceItem, len(normalized))
	for _, skill := range normalized {
		byID[skill.ID] = skill
	}
	return &SkillReadTool{skills: byID}
}

func NewSkillSearchTool(skills []SkillWorkspaceItem) *SkillSearchTool {
	return &SkillSearchTool{skills: normalizeSkillWorkspace(skills)}
}

func (t *SkillListTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "skill_list",
		Desc: "List structured skills enabled for the current conversation with bounded pagination. Use offset/next_offset to continue the skill list. When one item reports files_truncated, call skill_list with that skill_id and next_file_offset as offset to continue its exact file paths. Skill bodies are not automatically in context.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"skill_id": {
				Type:     schema.String,
				Desc:     "Optional skill ID. When set, paginate only that skill's allowed file paths.",
				Required: false,
			},
			"offset": {
				Type:     schema.Integer,
				Desc:     "Skill offset for the main list, or file offset when skill_id is set.",
				Required: false,
			},
			"max_items": {
				Type:     schema.Integer,
				Desc:     "Maximum skills or files to return. Defaults to 12 and is clamped to 24.",
				Required: false,
			},
			"max_chars": {
				Type:     schema.Integer,
				Desc:     "Maximum metadata characters to return. Defaults to 8000 and is clamped to 12000.",
				Required: false,
			},
		}),
	}, nil
}

func (t *SkillListTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var input SkillListInput
	if strings.TrimSpace(argumentsInJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
			return marshalSkillWorkspaceOutput(SkillListOutput{Error: "invalid input: " + err.Error(), Message: "skill_list input is invalid."})
		}
	}
	input.SkillID = strings.TrimSpace(input.SkillID)
	offset := maxInt(input.Offset, 0)
	maxItems := clampPositive(input.MaxItems, defaultSkillListItems, hardSkillListItems)
	maxChars := maxInt(clampPositive(input.MaxChars, defaultSkillListChars, hardSkillListChars), 512)
	if input.SkillID != "" {
		for _, skill := range t.skills {
			if skill.ID == input.SkillID {
				return marshalSkillWorkspaceOutput(buildSkillFilePage(skill, offset, maxItems, maxChars))
			}
		}
		return marshalSkillWorkspaceOutput(SkillListOutput{Error: "skill is not enabled", Message: "The requested skill is not enabled or visible in this conversation."})
	}
	return marshalSkillWorkspaceOutput(buildSkillListPage(t.skills, offset, maxItems, maxChars))
}

func buildSkillListPage(skills []SkillWorkspaceItem, offset, maxItems, maxChars int) SkillListOutput {
	if offset > len(skills) {
		offset = len(skills)
	}
	items := make([]SkillListItem, 0, maxItems)
	next := offset
	omittedFiles := 0
	for next < len(skills) && len(items) < maxItems {
		skill := skills[next]
		fileLimit := minInt(len(skill.Files), maxSkillListFiles)
		files := skillFileOutputs(skill.Files[:fileLimit])
		candidate := SkillListItem{
			ID:             truncateRunes(skill.ID, 160),
			Name:           truncateRunes(skill.Name, 200),
			Description:    truncateRunes(strings.TrimSpace(skill.Description), 500),
			Files:          files,
			FilesTruncated: fileLimit < len(skill.Files),
			NextFileOffset: fileLimit,
		}
		candidate = fitSkillListItem(candidate, 0, maxChars)
		if candidate.FilesTruncated {
			omittedFiles += len(skill.Files) - candidate.NextFileOffset
		}
		probe, _ := json.Marshal(SkillListOutput{Skills: append(items, candidate)})
		if len(items) > 0 && utf8.RuneCount(probe) > maxChars {
			break
		}
		items = append(items, candidate)
		next++
	}
	hasMore := next < len(skills)
	message := fmt.Sprintf("Returned %d enabled skill(s). Read relevant SKILL.md files through bounded pages before relying on them.", len(items))
	if hasMore || omittedFiles > 0 {
		message += " Continue with next_offset or a skill-specific file page when more metadata is needed."
	}
	return SkillListOutput{
		Skills:        items,
		Scope:         "skills",
		Truncated:     hasMore || omittedFiles > 0,
		OmittedSkills: len(skills) - next,
		OmittedFiles:  omittedFiles,
		StartOffset:   offset,
		NextOffset:    next,
		HasMore:       hasMore,
		Message:       message,
	}
}

func buildSkillFilePage(skill SkillWorkspaceItem, offset, maxItems, maxChars int) SkillListOutput {
	if offset > len(skill.Files) {
		offset = len(skill.Files)
	}
	end := minInt(offset+maxItems, len(skill.Files))
	files := skillFileOutputs(skill.Files[offset:end])
	item := SkillListItem{
		ID:             truncateRunes(skill.ID, 160),
		Name:           truncateRunes(skill.Name, 200),
		Description:    truncateRunes(strings.TrimSpace(skill.Description), 500),
		Files:          files,
		FilesTruncated: end < len(skill.Files),
		NextFileOffset: end,
	}
	item = fitSkillListItem(item, offset, maxChars)
	hasMore := item.NextFileOffset < len(skill.Files)
	return SkillListOutput{
		Skills:       []SkillListItem{item},
		Scope:        "files",
		SkillID:      skill.ID,
		Truncated:    hasMore,
		OmittedFiles: len(skill.Files) - item.NextFileOffset,
		StartOffset:  offset,
		NextOffset:   item.NextFileOffset,
		HasMore:      hasMore,
		Message:      "Returned a bounded page of allowed files for the selected skill.",
	}
}

func fitSkillListItem(item SkillListItem, baseOffset, maxChars int) SkillListItem {
	for len(item.Files) > 0 {
		probe, _ := json.Marshal(SkillListOutput{Skills: []SkillListItem{item}})
		if utf8.RuneCount(probe) <= maxChars {
			break
		}
		item.Files = item.Files[:len(item.Files)-1]
		item.FilesTruncated = true
		item.NextFileOffset = baseOffset + len(item.Files)
	}
	probe, _ := json.Marshal(SkillListOutput{Skills: []SkillListItem{item}})
	if utf8.RuneCount(probe) > maxChars {
		item.Description = ""
	}
	probe, _ = json.Marshal(SkillListOutput{Skills: []SkillListItem{item}})
	if utf8.RuneCount(probe) > maxChars {
		item.Name = ""
	}
	return item
}

func skillFileOutputs(files []model.SkillFile) []SkillFileToolOutput {
	out := make([]SkillFileToolOutput, 0, len(files))
	for _, file := range files {
		out = append(out, SkillFileToolOutput{
			Path: file.RelativePath,
			Kind: truncateRunes(file.Kind, 40),
			Size: file.Size,
		})
	}
	return out
}

func clampPositive(value, fallback, hard int) int {
	if value <= 0 {
		return fallback
	}
	return minInt(value, hard)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (t *SkillReadTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "skill_read",
		Desc: "Read a bounded page from an enabled structured skill. For every relevant SKILL.md, continue from next_offset until truncated=false before claiming the entry instructions were fully read or the skill was fully followed. If the run context budget blocks continuation, explicitly state that some instructions remain unread. Read references only when needed. Do not guess paths outside skill_list and do not treat skill files as executable code.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"skill_id": {
				Type:     schema.String,
				Desc:     "Skill ID from skill_list.",
				Required: true,
			},
			"path": {
				Type:     schema.String,
				Desc:     "Relative file path from skill_list, usually SKILL.md or references/*.md.",
				Required: true,
			},
			"offset": {
				Type:     schema.Integer,
				Desc:     "Character offset for continuing any skill file, including SKILL.md.",
				Required: false,
			},
			"max_chars": {
				Type:     schema.Integer,
				Desc:     "Maximum characters to return. Defaults to 5000 and is clamped to 16000.",
				Required: false,
			},
		}),
	}, nil
}

func (t *SkillReadTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var input SkillReadInput
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return marshalSkillWorkspaceOutput(SkillReadOutput{Error: "invalid input: " + err.Error(), Message: "skill_read input is invalid."})
	}
	input.SkillID = strings.TrimSpace(input.SkillID)
	path := normalizeSkillToolPath(input.Path)
	if input.SkillID == "" || path == "" {
		return marshalSkillWorkspaceOutput(SkillReadOutput{SkillID: input.SkillID, Path: path, Error: "skill_id and path are required", Message: "skill_read requires skill_id and path."})
	}
	skill, ok := t.skills[input.SkillID]
	if !ok {
		return marshalSkillWorkspaceOutput(SkillReadOutput{SkillID: input.SkillID, Path: path, Error: "skill is not enabled", Message: "The skill is not enabled or not visible in this conversation."})
	}
	file, ok := findSkillFile(skill.Files, path)
	if !ok {
		return marshalSkillWorkspaceOutput(SkillReadOutput{SkillID: input.SkillID, Path: path, Error: "file not found", Message: "The requested skill file does not exist. Use skill_list and SKILL.md paths exactly."})
	}
	content, err := readSkillStorageFile(file)
	if err != nil {
		return marshalSkillWorkspaceOutput(SkillReadOutput{SkillID: input.SkillID, Path: path, Kind: file.Kind, Error: err.Error(), Message: "Failed to read skill file."})
	}
	content = strings.TrimSpace(content)
	out := SkillReadOutput{SkillID: input.SkillID, Path: path, Kind: file.Kind}
	snippet := sliceFileReadRunes(content, clampOffset(input.Offset, utf8.RuneCountInString(content)), clampFileReadMaxChars(input.MaxChars, defaultSkillReadMaxChars, hardSkillReadMaxChars))
	out.Content = snippet.Content
	out.StartOffset = snippet.StartOffset
	out.NextOffset = snippet.NextOffset
	out.Truncated = snippet.Truncated
	if file.Kind == "entry" || path == "SKILL.md" {
		out.Message = "Returned a bounded SKILL.md page. Continue from next_offset until truncated=false before claiming the entry was fully read."
	} else {
		out.Message = "Returned a bounded reference skill file page."
	}
	return marshalSkillWorkspaceOutput(out)
}

func (t *SkillSearchTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "skill_search",
		Desc: "Search enabled structured skill files by keyword. Use this to find relevant reference passages after reading SKILL.md. Do not use search as a substitute for reading the entry instructions completely, and do not rely on a search hit without opening the relevant file when its surrounding context matters.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {
				Type:     schema.String,
				Desc:     "Keyword or phrase to search for in enabled skill files.",
				Required: true,
			},
			"skill_id": {
				Type:     schema.String,
				Desc:     "Optional skill ID to search inside one skill.",
				Required: false,
			},
			"max_results": {
				Type:     schema.Integer,
				Desc:     "Maximum matches to return. Defaults to 8 and is clamped to 20.",
				Required: false,
			},
		}),
	}, nil
}

func (t *SkillSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var input SkillSearchInput
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return marshalSkillWorkspaceOutput(SkillSearchOutput{Error: "invalid input: " + err.Error(), Message: "skill_search input is invalid."})
	}
	input.Query = strings.TrimSpace(input.Query)
	input.SkillID = strings.TrimSpace(input.SkillID)
	if input.Query == "" {
		return marshalSkillWorkspaceOutput(SkillSearchOutput{Error: "query is required", Message: "skill_search query is required."})
	}
	maxResults := input.MaxResults
	if maxResults <= 0 {
		maxResults = 8
	}
	if maxResults > 20 {
		maxResults = 20
	}
	tokens := queryTokens(input.Query)
	matches := make([]SkillSearchMatch, 0, maxResults)
	for _, skill := range t.skills {
		if input.SkillID != "" && skill.ID != input.SkillID {
			continue
		}
		for _, file := range skill.Files {
			content, err := readSkillStorageFile(file)
			if err != nil || strings.TrimSpace(content) == "" {
				continue
			}
			matches = append(matches, searchSkillFileText(skill.ID, file.RelativePath, content, tokens, maxResults-len(matches))...)
			if len(matches) >= maxResults {
				break
			}
		}
		if len(matches) >= maxResults {
			break
		}
	}
	message := fmt.Sprintf("Found %d skill file match(es).", len(matches))
	if len(matches) == 0 {
		message = "No enabled skill file matched the query."
	}
	return marshalSkillWorkspaceOutput(SkillSearchOutput{Query: input.Query, Results: matches, Message: message})
}

func normalizeSkillWorkspace(skills []SkillWorkspaceItem) []SkillWorkspaceItem {
	out := make([]SkillWorkspaceItem, 0, len(skills))
	for _, skill := range skills {
		skill.ID = strings.TrimSpace(skill.ID)
		if skill.ID == "" {
			continue
		}
		out = append(out, skill)
	}
	return out
}

func findSkillFile(files []model.SkillFile, path string) (model.SkillFile, bool) {
	for _, file := range files {
		if file.RelativePath == path {
			return file, true
		}
	}
	return model.SkillFile{}, false
}

func normalizeSkillToolPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		path = "SKILL.md"
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") {
		return ""
	}
	return clean
}

func readSkillStorageFile(file model.SkillFile) (string, error) {
	if strings.TrimSpace(file.StoragePath) == "" {
		return "", fmt.Errorf("skill file storage path is empty")
	}
	root, err := filepath.Abs(skillStorageRoot)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(file.StoragePath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("skill file path is outside storage root")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func searchSkillFileText(skillID, path, text string, tokens []string, limit int) []SkillSearchMatch {
	if limit <= 0 || len(tokens) == 0 {
		return nil
	}
	matches := make([]SkillSearchMatch, 0, limit)
	start := 0
	for len(matches) < limit {
		offset, ok := firstTokenMatchOffsetFrom(text, tokens, start)
		if !ok {
			break
		}
		snippet := sliceFileReadRunes(text, maxInt(offset-80, 0), 260)
		matches = append(matches, SkillSearchMatch{
			SkillID: skillID,
			Path:    path,
			Offset:  offset,
			Snippet: compactWhitespace(snippet.Content),
		})
		start = offset + 1
	}
	return matches
}

func marshalSkillWorkspaceOutput(out interface{}) (string, error) {
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("failed to marshal skill workspace output: %w", err)
	}
	return string(b), nil
}

var _ einoTool.InvokableTool = (*SkillListTool)(nil)
var _ einoTool.InvokableTool = (*SkillReadTool)(nil)
var _ einoTool.InvokableTool = (*SkillSearchTool)(nil)
