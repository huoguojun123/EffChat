package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/huoguojun123/effchat/internal/model"
	skillparser "github.com/huoguojun123/effchat/internal/skill"
)

func (s *SkillService) PreviewZip(data []byte) (*SkillZipPreviewResult, error) {
	parsed, report, err := skillparser.ImportZip(data)
	if err != nil {
		return nil, err
	}
	parsed = dedupeParsedSkills(parsed, &report)
	report.Imported = len(parsed)
	return &SkillZipPreviewResult{Skills: s.toSkillPreviews(parsed), Report: report}, nil
}

func (s *SkillService) ImportZip(userID int64, data []byte, selectedPaths []string, selectedFiles map[string][]string) (*SkillImportResult, error) {
	parsed, report, err := skillparser.ImportZipSelectedFiles(data, selectedPaths, selectedFiles)
	if err != nil {
		return nil, err
	}
	return s.persistParsed(userID, parsed, report, SkillSourceZip, nil, nil)
}

func (s *SkillService) PreviewGit(ctx context.Context, req *SkillGitPreviewRequest) (*SkillGitPreviewResult, error) {
	repoURL := strings.TrimSpace(req.URL)
	if err := validateGitURL(ctx, repoURL); err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(req.Ref)
	if ref != "" && !safeGitRef(ref) {
		return nil, fmt.Errorf("invalid git ref")
	}
	branches, defaultRef, err := listGitBranches(ctx, repoURL)
	if err != nil {
		return nil, err
	}
	selectedRef := selectGitRef(ref, defaultRef, branches)
	if selectedRef == "" {
		return nil, fmt.Errorf("no git branches found")
	}
	parsed, report, err := scanGitRef(ctx, repoURL, selectedRef, nil)
	if err != nil {
		return nil, err
	}
	parsed = dedupeParsedSkills(parsed, &report)
	report.Imported = len(parsed)
	return &SkillGitPreviewResult{
		Branches:    branches,
		SelectedRef: selectedRef,
		Skills:      s.toSkillPreviews(parsed),
		Report:      report,
	}, nil
}

func (s *SkillService) ImportGit(ctx context.Context, userID int64, req *SkillGitImportRequest) (*SkillImportResult, error) {
	repoURL := strings.TrimSpace(req.URL)
	if err := validateGitURL(ctx, repoURL); err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(req.Ref)
	if ref != "" && !safeGitRef(ref) {
		return nil, fmt.Errorf("invalid git ref")
	}
	if ref == "" {
		branches, defaultRef, err := listGitBranches(ctx, repoURL)
		if err != nil {
			return nil, err
		}
		ref = selectGitRef("", defaultRef, branches)
	}
	parsed, report, err := scanGitRef(ctx, repoURL, ref, req.SelectedFiles)
	if err != nil {
		return nil, err
	}
	parsed = skillparser.FilterParsedBySourcePaths(parsed, req.SelectedPaths, &report)
	sourceURL := repoURL
	var sourceRef *string
	if ref != "" {
		sourceRef = &ref
	}
	return s.persistParsed(userID, parsed, report, SkillSourceGit, &sourceURL, sourceRef)
}

func (s *SkillService) persistParsed(userID int64, parsed []skillparser.ParsedSkill, report skillparser.ImportReport, sourceType string, sourceURL, sourceRef *string) (*SkillImportResult, error) {
	parsed = dedupeParsedSkills(parsed, &report)
	report.Imported = len(parsed)
	resp := &SkillImportResult{Skills: []*SkillResponse{}, Report: report}
	for _, item := range parsed {
		sourcePath := item.SourcePath
		action := "create"
		enabled := true
		minGroupLevel := 0
		var createdBy *int64 = &userID
		if existing, err := s.skillRepo.Get(item.ID, true); err == nil {
			action = "update"
			enabled = existing.Enabled
			minGroupLevel = existing.MinGroupLevel
			createdBy = existing.CreatedBy
		}
		skill := &model.Skill{
			ID:              item.ID,
			Name:            item.Name,
			Description:     item.Description,
			SourceType:      sourceType,
			SourceURL:       sourceURL,
			SourceRef:       sourceRef,
			SourcePath:      &sourcePath,
			Checksum:        item.Checksum,
			PackageChecksum: item.PackageChecksum,
			EntryPath:       "SKILL.md",
			MinGroupLevel:   minGroupLevel,
			Enabled:         enabled,
			IsBuiltin:       false,
			CreatedBy:       createdBy,
		}
		record := s.buildImportRecord(action, skill, item, item.Files, report, &userID)
		if err := s.persistSkillPackage(skill, item.Files, record); err != nil {
			return nil, err
		}
		resp.Skills = append(resp.Skills, toSkillResponse(skill, true))
	}
	return resp, nil
}

func (s *SkillService) toSkillPreviews(parsed []skillparser.ParsedSkill) []SkillPreview {
	existing, _ := s.skillRepo.List(true)
	out := make([]SkillPreview, 0, len(parsed))
	for _, item := range parsed {
		matchSkill, matchType, defaultAction := matchExistingSkill(item, existing)
		preview := SkillPreview{
			ID:           item.ID,
			Name:         item.Name,
			Description:  item.Description,
			SourcePath:   item.SourcePath,
			Checksum:     item.Checksum,
			Dependencies: item.Dependencies,
			Files:        parsedSkillFileResponsesForPreview(item.SourcePath, previewFiles(item)),
			EntryPreview: limitRunes(item.Content, skillEntryPreviewRunes),
		}
		if len([]rune(item.Content)) > skillEntryPreviewRunes {
			preview.EntryTruncated = true
		}
		if matchSkill != nil {
			preview.ExistingSkill = toSkillResponse(matchSkill, true)
			preview.MatchType = matchType
			preview.DefaultAction = defaultAction
		} else {
			preview.DefaultAction = "create"
		}
		out = append(out, preview)
	}
	return out
}

func previewFiles(item skillparser.ParsedSkill) []skillparser.ParsedSkillFile {
	if len(item.AvailableFiles) > 0 {
		return item.AvailableFiles
	}
	return item.Files
}

func parsedSkillFileResponsesForPreview(entryPath string, files []skillparser.ParsedSkillFile) []SkillFileResponse {
	out := make([]SkillFileResponse, 0, len(files))
	for _, file := range files {
		path := file.Path
		if rel, err := packageRelativePath(entryPath, file.Path); err == nil {
			path = rel
		}
		out = append(out, SkillFileResponse{
			Path:            path,
			Kind:            file.Kind,
			Size:            file.Size,
			Checksum:        file.Checksum,
			Reason:          file.Reason,
			SelectedDefault: file.SelectedDefault,
		})
	}
	return out
}

func dedupeParsedSkills(parsed []skillparser.ParsedSkill, report *skillparser.ImportReport) []skillparser.ParsedSkill {
	byID := map[string]int{}
	out := make([]skillparser.ParsedSkill, 0, len(parsed))
	for _, item := range parsed {
		if item.ID == "" {
			continue
		}
		if index, ok := byID[item.ID]; ok {
			if report != nil {
				report.Deduped = append(report.Deduped, fmt.Sprintf("%s 覆盖 %s，id=%s", item.SourcePath, out[index].SourcePath, item.ID))
			}
			out[index] = item
			continue
		}
		byID[item.ID] = len(out)
		out = append(out, item)
	}
	return out
}
