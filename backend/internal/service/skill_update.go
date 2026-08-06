package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	skillparser "github.com/huoguojun123/EffChat/internal/skill"
)

const skillEntryPreviewRunes = 3200

// Skill 更新刻意建模为“重新导入 + 对比 + 管理员确认”，而不是后台自动同步。
//
// 原因有三点：
// 1. Skill 包的入口和 references 由管理员选择，自动同步无法知道新增文件是否可信。
// 2. 本地 skill_id 绑定会话启用列表和分级组权限，更新时必须保持稳定。
// 3. Git/Zip/同名不同 ID 都可能出现身份歧义，必须通过 preview 暴露给管理员确认。
//
// 因此本文件只负责 preview、候选匹配、文件差异和最终覆盖当前包；运行态仍然只读
// skills、skill_files 和落盘后的稳定包目录。

type skillFileManifestItem struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Checksum string `json:"checksum"`
}

func (s *SkillService) ListImportRecords(id string) ([]SkillImportRecordResponse, error) {
	if _, err := s.skillRepo.Get(id, true); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, newSkillError(SkillErrorNotFound, "Skill not found", err)
		}
		return nil, err
	}
	records, err := s.skillRepo.ListImportRecords(id, 50)
	if err != nil {
		return nil, err
	}
	return toSkillImportRecordResponses(records), nil
}

func (s *SkillService) PreviewGitUpdate(ctx context.Context, id string, req *SkillUpdateGitPreviewRequest) (*SkillUpdatePreviewResult, error) {
	current, err := s.skillRepo.Get(id, true)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, newSkillError(SkillErrorNotFound, "Skill not found", err)
		}
		return nil, err
	}
	if current.SourceType != SkillSourceGit || current.SourceURL == nil || strings.TrimSpace(*current.SourceURL) == "" {
		return nil, newSkillError(SkillErrorConflict, "Skill is not Git-imported", nil)
	}
	ref := strings.TrimSpace(req.Ref)
	if ref == "" && current.SourceRef != nil {
		ref = strings.TrimSpace(*current.SourceRef)
	}
	if ref != "" && !safeGitRef(ref) {
		return nil, newSkillError(SkillErrorInvalid, "invalid Git ref", nil)
	}
	if ref == "" {
		branches, defaultRef, err := listGitBranches(ctx, *current.SourceURL)
		if err != nil {
			return nil, newSkillError(SkillErrorSourceUnavailable, "Skill Git source is unavailable", err)
		}
		ref = selectGitRef("", defaultRef, branches)
	}
	parsed, report, err := scanGitRef(ctx, *current.SourceURL, ref, nil)
	if err != nil {
		return nil, newSkillError(SkillErrorSourceUnavailable, "Skill Git source could not be scanned", err)
	}
	return s.buildUpdatePreview(current, parsed, report)
}

func (s *SkillService) PreviewZipUpdate(id string, data []byte) (*SkillUpdatePreviewResult, error) {
	current, err := s.skillRepo.Get(id, true)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, newSkillError(SkillErrorNotFound, "Skill not found", err)
		}
		return nil, err
	}
	parsed, report, err := skillparser.ImportZip(data)
	if err != nil {
		return nil, newSkillError(SkillErrorInvalid, "invalid Skill archive", err)
	}
	return s.buildUpdatePreview(current, parsed, report)
}

func (s *SkillService) UpdateGit(ctx context.Context, userID int64, id string, req *SkillUpdateApplyRequest) (*SkillImportResult, error) {
	current, err := s.skillRepo.Get(id, true)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, newSkillError(SkillErrorNotFound, "Skill not found", err)
		}
		return nil, err
	}
	repoURL := strings.TrimSpace(req.URL)
	if repoURL == "" && current.SourceURL != nil {
		repoURL = strings.TrimSpace(*current.SourceURL)
	}
	if repoURL == "" {
		return nil, newSkillError(SkillErrorInvalid, "Git repository URL is required", nil)
	}
	if err := validateGitURL(ctx, repoURL); err != nil {
		return nil, newSkillError(SkillErrorInvalid, "invalid Git repository URL", err)
	}
	ref := strings.TrimSpace(req.Ref)
	if ref == "" && current.SourceRef != nil {
		ref = strings.TrimSpace(*current.SourceRef)
	}
	if ref != "" && !safeGitRef(ref) {
		return nil, newSkillError(SkillErrorInvalid, "invalid Git ref", nil)
	}
	selected := map[string][]string{req.SourcePath: req.SelectedFiles}
	parsed, report, err := scanGitRef(ctx, repoURL, ref, selected)
	if err != nil {
		return nil, newSkillError(SkillErrorSourceUnavailable, "Skill Git source could not be scanned", err)
	}
	parsed = skillparser.FilterParsedBySourcePaths(parsed, []string{req.SourcePath}, &report)
	if len(parsed) == 0 {
		return nil, newSkillError(SkillErrorConflict, "selected Skill candidate is no longer available", nil)
	}
	sourceURL := repoURL
	var sourceRef *string
	if ref != "" {
		sourceRef = &ref
	}
	return s.persistSkillUpdate(ctx, userID, current, parsed[0], report, SkillSourceGit, &sourceURL, sourceRef)
}

func (s *SkillService) UpdateZip(userID int64, id string, data []byte, req *SkillUpdateApplyRequest) (*SkillImportResult, error) {
	return s.UpdateZipContext(context.Background(), userID, id, data, req)
}

func (s *SkillService) UpdateZipContext(ctx context.Context, userID int64, id string, data []byte, req *SkillUpdateApplyRequest) (*SkillImportResult, error) {
	current, err := s.skillRepo.Get(id, true)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, newSkillError(SkillErrorNotFound, "Skill not found", err)
		}
		return nil, err
	}
	selected := map[string][]string{req.SourcePath: req.SelectedFiles}
	parsed, report, err := skillparser.ImportZipSelectedFiles(data, []string{req.SourcePath}, selected)
	if err != nil {
		return nil, newSkillError(SkillErrorInvalid, "invalid Skill archive selection", err)
	}
	if len(parsed) == 0 {
		return nil, newSkillError(SkillErrorConflict, "selected Skill candidate is no longer available", nil)
	}
	return s.persistSkillUpdate(ctx, userID, current, parsed[0], report, SkillSourceZip, nil, nil)
}

func (s *SkillService) persistSkillUpdate(ctx context.Context, userID int64, current *model.Skill, item skillparser.ParsedSkill, report skillparser.ImportReport, sourceType string, sourceURL, sourceRef *string) (*SkillImportResult, error) {
	sourcePath := item.SourcePath
	skill := &model.Skill{
		ID:              current.ID,
		Name:            item.Name,
		Description:     item.Description,
		SourceType:      sourceType,
		SourceURL:       sourceURL,
		SourceRef:       sourceRef,
		SourcePath:      &sourcePath,
		Checksum:        item.Checksum,
		PackageChecksum: item.PackageChecksum,
		EntryPath:       "SKILL.md",
		MinGroupLevel:   current.MinGroupLevel,
		Enabled:         current.Enabled,
		IsBuiltin:       false,
		CreatedBy:       current.CreatedBy,
	}
	record := s.buildImportRecord("update", skill, item, item.Files, report, &userID)
	if err := s.persistSkillPackage(ctx, skill, item.Files, record, repository.SkillGovernanceMutation{
		Action: "import", ActorType: "import", ActorUserID: userID,
		Reason: "admin updated Skill from imported package",
	}); err != nil {
		return nil, err
	}
	return &SkillImportResult{Skills: []*SkillResponse{toSkillResponse(skill, true)}, Report: report}, nil
}

func (s *SkillService) buildUpdatePreview(current *model.Skill, parsed []skillparser.ParsedSkill, report skillparser.ImportReport) (*SkillUpdatePreviewResult, error) {
	parsed = dedupeParsedSkills(parsed, &report)
	report.Imported = len(parsed)
	previews, err := s.toSkillPreviews(parsed)
	if err != nil {
		return nil, err
	}
	selectedSourcePath, matchType := selectUpdateCandidate(current, parsed)
	var changes []SkillFileChange
	var defaultSelected []string
	if selectedSourcePath != "" {
		for _, preview := range previews {
			if preview.SourcePath == selectedSourcePath {
				changes = compareSkillFiles(current.Files, preview.Files)
				defaultSelected = defaultSelectedReferencePaths(current.Files, preview.Files)
				break
			}
		}
	}
	currentEntry, truncated, err := s.currentEntryPreview(current.ID)
	if err != nil {
		return nil, err
	}
	return &SkillUpdatePreviewResult{
		Current:               toSkillResponse(current, true),
		Candidates:            previews,
		SelectedSourcePath:    selectedSourcePath,
		MatchType:             matchType,
		DefaultSelectedFiles:  defaultSelected,
		FileChanges:           changes,
		CurrentEntryPreview:   currentEntry,
		CurrentEntryTruncated: truncated,
		Report:                report,
	}, nil
}

func selectUpdateCandidate(current *model.Skill, parsed []skillparser.ParsedSkill) (string, string) {
	// 匹配优先级体现身份可信度：ID 是最强身份；name 适合处理上游改路径；
	// source_path 只作为最后兜底，因为同一仓库结构调整时路径最容易变化。
	for _, item := range parsed {
		if item.ID == current.ID {
			return item.SourcePath, "id"
		}
	}
	for _, item := range parsed {
		if sameSkillName(item.Name, current.Name) {
			return item.SourcePath, "name"
		}
	}
	if current.SourcePath != nil {
		for _, item := range parsed {
			if item.SourcePath == *current.SourcePath {
				return item.SourcePath, "path"
			}
		}
	}
	return "", ""
}

func matchExistingSkill(item skillparser.ParsedSkill, existing []*model.Skill) (*model.Skill, string, string) {
	for _, skill := range existing {
		if skill.ID == item.ID {
			return skill, "id", "update"
		}
	}
	for _, skill := range existing {
		if sameSkillName(skill.Name, item.Name) {
			return skill, "name", "review"
		}
	}
	return nil, "", "create"
}

func sameSkillName(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func compareSkillFiles(current []model.SkillFile, candidate []SkillFileResponse) []SkillFileChange {
	currentByPath := map[string]model.SkillFile{}
	for _, file := range current {
		currentByPath[file.RelativePath] = file
	}
	seen := map[string]struct{}{}
	changes := make([]SkillFileChange, 0, len(candidate)+len(current))
	for _, file := range candidate {
		seen[file.Path] = struct{}{}
		change := SkillFileChange{
			Path:            file.Path,
			Kind:            file.Kind,
			NewChecksum:     file.Checksum,
			NewSize:         file.Size,
			Reason:          file.Reason,
			SelectedDefault: file.SelectedDefault,
		}
		if file.Kind == "entry" || file.Path == "SKILL.md" {
			change.Status = "entry"
			change.SelectedDefault = true
			if old, ok := currentByPath[file.Path]; ok {
				change.OldChecksum = old.Checksum
				change.OldSize = old.Size
			}
			changes = append(changes, change)
			continue
		}
		if old, ok := currentByPath[file.Path]; ok {
			change.OldChecksum = old.Checksum
			change.OldSize = old.Size
			change.SelectedDefault = true
			if file.Checksum != "" && file.Checksum == old.Checksum {
				change.Status = "unchanged"
			} else if file.Checksum != "" {
				change.Status = "modified"
			} else {
				change.Status = "same_path"
			}
		} else {
			change.Status = "added"
		}
		changes = append(changes, change)
	}
	for _, file := range current {
		if file.Kind == "entry" || file.RelativePath == "SKILL.md" {
			continue
		}
		if _, ok := seen[file.RelativePath]; ok {
			continue
		}
		changes = append(changes, SkillFileChange{
			Path:        file.RelativePath,
			Kind:        file.Kind,
			Status:      "missing",
			OldChecksum: file.Checksum,
			OldSize:     file.Size,
		})
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Kind != changes[j].Kind {
			return changes[i].Kind == "entry"
		}
		return changes[i].Path < changes[j].Path
	})
	return changes
}

func defaultSelectedReferencePaths(current []model.SkillFile, candidate []SkillFileResponse) []string {
	currentPaths := map[string]struct{}{}
	for _, file := range current {
		if file.Kind != "entry" && file.RelativePath != "SKILL.md" {
			currentPaths[file.RelativePath] = struct{}{}
		}
	}
	var out []string
	for _, file := range candidate {
		if file.Kind == "entry" || file.Path == "SKILL.md" {
			continue
		}
		if _, ok := currentPaths[file.Path]; ok {
			out = append(out, file.Path)
		}
	}
	sort.Strings(out)
	return out
}

func (s *SkillService) currentEntryPreview(skillID string) (string, bool, error) {
	content, _, err := s.readSkillFile(skillID, "SKILL.md")
	if err != nil {
		return "", false, err
	}
	preview, truncated := truncateRunes(content, skillEntryPreviewRunes)
	return preview, truncated, nil
}

func truncateRunes(value string, limit int) (string, bool) {
	runes := []rune(value)
	if len(runes) <= limit {
		return value, false
	}
	return string(runes[:limit]), true
}

func limitRunes(value string, limit int) string {
	out, _ := truncateRunes(value, limit)
	return out
}

func (s *SkillService) buildImportRecord(action string, skill *model.Skill, item skillparser.ParsedSkill, files []skillparser.ParsedSkillFile, report skillparser.ImportReport, userID *int64) *model.SkillImportRecord {
	sourcePath := item.SourcePath
	if sourcePath == "" && skill.SourcePath != nil {
		sourcePath = *skill.SourcePath
	}
	return &model.SkillImportRecord{
		Action:          action,
		SkillID:         skill.ID,
		SourceType:      skill.SourceType,
		SourceURL:       skill.SourceURL,
		SourceRef:       skill.SourceRef,
		SourcePath:      sourcePath,
		UpstreamSkillID: item.ID,
		UpstreamName:    item.Name,
		PackageChecksum: item.PackageChecksum,
		SelectedFiles:   marshalSelectedSkillFiles(files),
		ImportReport:    marshalJSON(report, "{}"),
		CreatedBy:       userID,
	}
}

func marshalSelectedSkillFiles(files []skillparser.ParsedSkillFile) []byte {
	if len(files) == 0 {
		return []byte("[]")
	}
	entryPath := files[0].Path
	var selected []string
	for _, file := range files {
		if file.Kind == "entry" {
			continue
		}
		rel, err := packageRelativePath(entryPath, file.Path)
		if err != nil {
			rel = filepath.ToSlash(file.Path)
		}
		selected = append(selected, rel)
	}
	sort.Strings(selected)
	return marshalJSON(selected, "[]")
}

func marshalSkillFileManifest(files []model.SkillFile) []byte {
	out := make([]skillFileManifestItem, 0, len(files))
	for _, file := range files {
		out = append(out, skillFileManifestItem{
			Path:     file.RelativePath,
			Kind:     file.Kind,
			Size:     file.Size,
			Checksum: file.Checksum,
		})
	}
	return marshalJSON(out, "[]")
}

func marshalJSON(value interface{}, fallback string) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte(fallback)
	}
	return raw
}

func toSkillImportRecordResponses(records []model.SkillImportRecord) []SkillImportRecordResponse {
	out := make([]SkillImportRecordResponse, 0, len(records))
	for _, record := range records {
		out = append(out, SkillImportRecordResponse{
			ID:              record.ID,
			SkillID:         record.SkillID,
			Action:          record.Action,
			SourceType:      record.SourceType,
			SourceURL:       record.SourceURL,
			SourceRef:       record.SourceRef,
			SourcePath:      record.SourcePath,
			UpstreamSkillID: record.UpstreamSkillID,
			UpstreamName:    record.UpstreamName,
			PackageChecksum: record.PackageChecksum,
			SelectedFiles:   unmarshalJSONValue(record.SelectedFiles, []interface{}{}),
			FileManifest:    unmarshalJSONValue(record.FileManifest, []interface{}{}),
			ImportReport:    unmarshalJSONValue(record.ImportReport, map[string]interface{}{}),
			CreatedBy:       record.CreatedBy,
			CreatedAt:       record.CreatedAt.Format(time.RFC3339),
		})
	}
	return out
}

func unmarshalJSONValue(raw []byte, fallback interface{}) interface{} {
	if len(raw) == 0 {
		return fallback
	}
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback
	}
	return value
}
