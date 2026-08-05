package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	skillparser "github.com/huoguojun123/EffChat/internal/skill"
)

func (s *SkillService) PreviewZip(data []byte) (*SkillZipPreviewResult, error) {
	parsed, report, err := skillparser.ImportZip(data)
	if err != nil {
		return nil, newSkillError(SkillErrorInvalid, "invalid Skill archive", err)
	}
	parsed = dedupeParsedSkills(parsed, &report)
	report.Imported = len(parsed)
	previews, err := s.toSkillPreviews(parsed)
	if err != nil {
		return nil, err
	}
	return &SkillZipPreviewResult{Skills: previews, Report: report}, nil
}

func (s *SkillService) ImportZip(userID int64, data []byte, selectedPaths []string, selectedFiles map[string][]string, targetSkillIDs map[string]string) (*SkillImportResult, error) {
	parsed, report, err := skillparser.ImportZipSelectedFiles(data, selectedPaths, selectedFiles)
	if err != nil {
		return nil, newSkillError(SkillErrorInvalid, "invalid Skill archive selection", err)
	}
	return s.persistParsed(userID, parsed, report, SkillSourceZip, nil, nil, targetSkillIDs)
}

func (s *SkillService) PreviewGit(ctx context.Context, req *SkillGitPreviewRequest) (*SkillGitPreviewResult, error) {
	repoURL := strings.TrimSpace(req.URL)
	if err := validateGitURL(ctx, repoURL); err != nil {
		return nil, newSkillError(SkillErrorInvalid, "invalid Git repository URL", err)
	}
	ref := strings.TrimSpace(req.Ref)
	if ref != "" && !safeGitRef(ref) {
		return nil, newSkillError(SkillErrorInvalid, "invalid Git ref", nil)
	}
	branches, defaultRef, err := listGitBranches(ctx, repoURL)
	if err != nil {
		return nil, newSkillError(SkillErrorSourceUnavailable, "Skill Git source is unavailable", err)
	}
	selectedRef := selectGitRef(ref, defaultRef, branches)
	if selectedRef == "" {
		return nil, newSkillError(SkillErrorSourceUnavailable, "Skill Git source has no branches", nil)
	}
	parsed, report, err := scanGitRef(ctx, repoURL, selectedRef, nil)
	if err != nil {
		return nil, newSkillError(SkillErrorSourceUnavailable, "Skill Git source could not be scanned", err)
	}
	parsed = dedupeParsedSkills(parsed, &report)
	report.Imported = len(parsed)
	previews, err := s.toSkillPreviews(parsed)
	if err != nil {
		return nil, err
	}
	return &SkillGitPreviewResult{
		Branches:    branches,
		SelectedRef: selectedRef,
		Skills:      previews,
		Report:      report,
	}, nil
}

func (s *SkillService) ImportGit(ctx context.Context, userID int64, req *SkillGitImportRequest) (*SkillImportResult, error) {
	repoURL := strings.TrimSpace(req.URL)
	if err := validateGitURL(ctx, repoURL); err != nil {
		return nil, newSkillError(SkillErrorInvalid, "invalid Git repository URL", err)
	}
	ref := strings.TrimSpace(req.Ref)
	if ref != "" && !safeGitRef(ref) {
		return nil, newSkillError(SkillErrorInvalid, "invalid Git ref", nil)
	}
	if ref == "" {
		branches, defaultRef, err := listGitBranches(ctx, repoURL)
		if err != nil {
			return nil, newSkillError(SkillErrorSourceUnavailable, "Skill Git source is unavailable", err)
		}
		ref = selectGitRef("", defaultRef, branches)
	}
	parsed, report, err := scanGitRef(ctx, repoURL, ref, req.SelectedFiles)
	if err != nil {
		return nil, newSkillError(SkillErrorSourceUnavailable, "Skill Git source could not be scanned", err)
	}
	parsed = skillparser.FilterParsedBySourcePaths(parsed, req.SelectedPaths, &report)
	sourceURL := repoURL
	var sourceRef *string
	if ref != "" {
		sourceRef = &ref
	}
	return s.persistParsed(userID, parsed, report, SkillSourceGit, &sourceURL, sourceRef, req.TargetSkillIDs)
}

func (s *SkillService) persistParsed(userID int64, parsed []skillparser.ParsedSkill, report skillparser.ImportReport, sourceType string, sourceURL, sourceRef *string, targetSkillIDs map[string]string) (*SkillImportResult, error) {
	parsed = dedupeParsedSkills(parsed, &report)
	report.Imported = len(parsed)
	resp := &SkillImportResult{Skills: []*SkillResponse{}, Report: report}
	packages := make([]preparedSkillPackage, 0, len(parsed))
	seenTargets := make(map[string]struct{}, len(parsed))
	for _, item := range parsed {
		sourcePath := item.SourcePath
		targetID := strings.TrimSpace(targetSkillIDs[sourcePath])
		if targetID == "" {
			targetID = item.ID
		}
		if _, duplicate := seenTargets[targetID]; duplicate {
			return nil, newSkillError(SkillErrorInvalid, "multiple imported Skills target the same Skill", nil)
		}
		seenTargets[targetID] = struct{}{}
		action := "create"
		enabled := true
		minGroupLevel := 0
		var createdBy *int64 = &userID
		if existing, err := s.skillRepo.Get(targetID, true); err == nil {
			action = "update"
			enabled = existing.Enabled
			minGroupLevel = existing.MinGroupLevel
			createdBy = existing.CreatedBy
			if existing.PackageChecksum == item.PackageChecksum {
				resp.Skills = append(resp.Skills, toSkillResponse(existing, true))
				resp.Unchanged = append(resp.Unchanged, existing.ID)
				continue
			}
		} else if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		} else if _, explicitTarget := targetSkillIDs[sourcePath]; explicitTarget {
			return nil, newSkillError(SkillErrorNotFound, "target Skill does not exist", err)
		}
		skill := &model.Skill{
			ID:              targetID,
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
		packages = append(packages, preparedSkillPackage{
			skill: skill, parsedFiles: item.Files, record: record,
			mutation: repository.SkillGovernanceMutation{
				Action: "import", ActorType: "import", ActorUserID: userID,
				Reason: "admin imported Skill package",
			},
		})
	}
	if err := s.persistSkillPackages(packages); err != nil {
		return nil, err
	}
	for _, item := range packages {
		resp.Skills = append(resp.Skills, toSkillResponse(item.skill, true))
		if item.record.Action == "create" {
			resp.Created = append(resp.Created, item.skill.ID)
		} else {
			resp.Updated = append(resp.Updated, item.skill.ID)
		}
	}
	return resp, nil
}

func (s *SkillService) toSkillPreviews(parsed []skillparser.ParsedSkill) ([]SkillPreview, error) {
	existing, err := s.skillRepo.List(true)
	if err != nil {
		return nil, err
	}
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
	return out, nil
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
