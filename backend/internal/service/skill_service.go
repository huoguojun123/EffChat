package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	skillparser "github.com/huoguojun123/EffChat/internal/skill"
)

const (
	SkillSourceBuiltin = "builtin"
	SkillSourceManual  = "manual"
	SkillSourceGit     = "git"
	SkillSourceZip     = "zip"
)

type SkillService struct {
	skillRepo         *repository.SkillRepository
	userRepo          *repository.UserRepository
	sessionRepo       *repository.SessionRepository
	governanceRepo    *repository.GovernanceRepository
	packageMu         sync.Mutex
	cleanupMu         sync.Mutex
	cleanupTimer      *time.Timer
	cleanupGeneration uint64
}

func NewSkillService(skillRepo *repository.SkillRepository, userRepo *repository.UserRepository, sessionRepo *repository.SessionRepository) *SkillService {
	return &SkillService{skillRepo: skillRepo, userRepo: userRepo, sessionRepo: sessionRepo}
}

func (s *SkillService) SetGovernanceRepository(repo *repository.GovernanceRepository) {
	s.governanceRepo = repo
}

type SkillResponse struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	SourceType      string              `json:"source_type"`
	SourceURL       *string             `json:"source_url,omitempty"`
	SourceRef       *string             `json:"source_ref,omitempty"`
	SourcePath      *string             `json:"source_path,omitempty"`
	Checksum        string              `json:"checksum"`
	PackageChecksum string              `json:"package_checksum"`
	EntryPath       string              `json:"entry_path"`
	MinGroupLevel   int                 `json:"min_group_level"`
	Files           []SkillFileResponse `json:"files"`
	Enabled         bool                `json:"enabled"`
	IsBuiltin       bool                `json:"is_builtin"`
	Authorized      bool                `json:"authorized"`
	CreatedBy       *int64              `json:"created_by,omitempty"`
	CreatedAt       string              `json:"created_at"`
	UpdatedAt       string              `json:"updated_at"`
}

type SkillFileResponse struct {
	Path            string `json:"path"`
	Kind            string `json:"kind"`
	Size            int64  `json:"size"`
	Checksum        string `json:"checksum"`
	Reason          string `json:"reason,omitempty"`
	SelectedDefault bool   `json:"selected_default,omitempty"`
}

type SkillImportRecordResponse struct {
	ID              int64       `json:"id"`
	SkillID         string      `json:"skill_id"`
	Action          string      `json:"action"`
	SourceType      string      `json:"source_type"`
	SourceURL       *string     `json:"source_url,omitempty"`
	SourceRef       *string     `json:"source_ref,omitempty"`
	SourcePath      string      `json:"source_path"`
	UpstreamSkillID string      `json:"upstream_skill_id"`
	UpstreamName    string      `json:"upstream_name"`
	PackageChecksum string      `json:"package_checksum"`
	SelectedFiles   interface{} `json:"selected_files"`
	FileManifest    interface{} `json:"file_manifest"`
	ImportReport    interface{} `json:"import_report"`
	CreatedBy       *int64      `json:"created_by,omitempty"`
	CreatedAt       string      `json:"created_at"`
}

type SkillInput struct {
	ID            string           `json:"id"`
	Name          string           `json:"name" binding:"required"`
	Description   string           `json:"description"`
	EntryContent  string           `json:"entry_content" binding:"required"`
	Files         []SkillFileInput `json:"files"`
	Enabled       *bool            `json:"enabled"`
	MinGroupLevel int              `json:"min_group_level"`
}

type SkillUpdateInput struct {
	Name          *string          `json:"name"`
	Description   *string          `json:"description"`
	EntryContent  *string          `json:"entry_content"`
	Files         []SkillFileInput `json:"files"`
	Enabled       *bool            `json:"enabled"`
	MinGroupLevel *int             `json:"min_group_level"`
}

type SkillFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type SkillGitImportRequest struct {
	URL            string              `json:"url" binding:"required"`
	Ref            string              `json:"ref"`
	SelectedPaths  []string            `json:"selected_paths"`
	SelectedFiles  map[string][]string `json:"selected_files"`
	TargetSkillIDs map[string]string   `json:"target_skill_ids"`
}

type SkillGitPreviewRequest struct {
	URL string `json:"url" binding:"required"`
	Ref string `json:"ref"`
}

type SkillPreview struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Description    string              `json:"description"`
	SourcePath     string              `json:"source_path"`
	Checksum       string              `json:"checksum"`
	Dependencies   []string            `json:"dependencies"`
	Files          []SkillFileResponse `json:"files"`
	EntryPreview   string              `json:"entry_preview,omitempty"`
	EntryTruncated bool                `json:"entry_truncated,omitempty"`
	ExistingSkill  *SkillResponse      `json:"existing_skill,omitempty"`
	MatchType      string              `json:"match_type,omitempty"`
	DefaultAction  string              `json:"default_action,omitempty"`
}

type SkillGitPreviewResult struct {
	Branches    []string                 `json:"branches"`
	SelectedRef string                   `json:"selected_ref"`
	Skills      []SkillPreview           `json:"skills"`
	Report      skillparser.ImportReport `json:"report"`
}

type SkillZipPreviewResult struct {
	Skills []SkillPreview           `json:"skills"`
	Report skillparser.ImportReport `json:"report"`
}

type SkillImportResult struct {
	Skills    []*SkillResponse         `json:"skills"`
	Created   []string                 `json:"created"`
	Updated   []string                 `json:"updated"`
	Unchanged []string                 `json:"unchanged"`
	Report    skillparser.ImportReport `json:"report"`
}

type preparedSkillPackage struct {
	skill       *model.Skill
	parsedFiles []skillparser.ParsedSkillFile
	record      *model.SkillImportRecord
	patch       repository.SkillMetadataPatch
	mutation    repository.SkillGovernanceMutation
}

type SkillUpdateGitPreviewRequest struct {
	Ref string `json:"ref"`
}

type SkillUpdateApplyRequest struct {
	SourcePath    string   `json:"source_path" binding:"required"`
	SelectedFiles []string `json:"selected_files"`
	Ref           string   `json:"ref"`
	URL           string   `json:"url"`
}

type SkillUpdatePreviewResult struct {
	Current               *SkillResponse           `json:"current"`
	Candidates            []SkillPreview           `json:"candidates"`
	SelectedSourcePath    string                   `json:"selected_source_path"`
	MatchType             string                   `json:"match_type"`
	DefaultSelectedFiles  []string                 `json:"default_selected_files"`
	FileChanges           []SkillFileChange        `json:"file_changes"`
	CurrentEntryPreview   string                   `json:"current_entry_preview"`
	CurrentEntryTruncated bool                     `json:"current_entry_truncated"`
	Report                skillparser.ImportReport `json:"report"`
}

type SkillFileChange struct {
	Path            string `json:"path"`
	Kind            string `json:"kind"`
	Status          string `json:"status"`
	OldChecksum     string `json:"old_checksum,omitempty"`
	NewChecksum     string `json:"new_checksum,omitempty"`
	OldSize         int64  `json:"old_size,omitempty"`
	NewSize         int64  `json:"new_size,omitempty"`
	Reason          string `json:"reason,omitempty"`
	SelectedDefault bool   `json:"selected_default,omitempty"`
}

type UserSkillsRequest struct {
	Skills []string `json:"skills"`
}

type SessionSkillsRequest struct {
	Skills []string `json:"skills"`
}

type SkillInstruction struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Files       []model.SkillFile `json:"files"`
}

func (s *SkillService) ListAdmin() ([]*SkillResponse, error) {
	skills, err := s.skillRepo.List(true)
	if err != nil {
		return nil, err
	}
	return toSkillResponses(skills, true), nil
}

func (s *SkillService) ListForUser(userID int64) ([]*SkillResponse, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, err
	}
	groupLevel := 0
	if user.Role != "admin" {
		groupLevel, err = s.userRepo.GetGroupLevel(userID)
		if err != nil {
			return nil, err
		}
	}
	skills, err := s.skillRepo.List(false)
	if err != nil {
		return nil, err
	}
	resp := make([]*SkillResponse, 0, len(skills))
	for _, skill := range skills {
		ok := skillAllowedForUser(user, groupLevel, skill)
		if ok {
			resp = append(resp, toSkillResponse(skill, true))
		}
	}
	return resp, nil
}

func (s *SkillService) CreateManual(userID int64, req *SkillInput) (*SkillResponse, error) {
	return s.CreateManualContext(context.Background(), userID, req)
}

func (s *SkillService) CreateManualContext(ctx context.Context, userID int64, req *SkillInput) (*SkillResponse, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	parsed, err := parseManualSkill(req)
	if err != nil {
		return nil, newSkillError(SkillErrorInvalid, "invalid Skill package", err)
	}
	id := normalizeSkillID(req.ID)
	if id == "" {
		id = normalizeSkillID(req.Name)
	}
	if id == "" {
		return nil, newSkillError(SkillErrorInvalid, "invalid Skill id", nil)
	}
	parsed.ID = id
	parsed.Name = strings.TrimSpace(req.Name)
	if strings.TrimSpace(req.Description) != "" {
		parsed.Description = strings.TrimSpace(req.Description)
	}
	skill := &model.Skill{
		ID:              parsed.ID,
		Name:            parsed.Name,
		Description:     parsed.Description,
		SourceType:      SkillSourceManual,
		SourcePath:      &parsed.SourcePath,
		Checksum:        parsed.Checksum,
		PackageChecksum: parsed.PackageChecksum,
		EntryPath:       "SKILL.md",
		MinGroupLevel:   max(0, req.MinGroupLevel),
		Enabled:         enabled,
		IsBuiltin:       false,
		CreatedBy:       &userID,
	}
	action := "create"
	if _, err := s.skillRepo.Get(skill.ID, true); err == nil {
		action = "update"
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	record := s.buildImportRecord(action, skill, parsed, parsed.Files, skillparser.ImportReport{}, &userID)
	mutation := repository.SkillGovernanceMutation{
		Action: action, ActorType: "admin", ActorUserID: userID,
		Reason: normalizeGovernanceReason("", "admin saved manual Skill package"),
	}
	if err := s.persistSkillPackage(ctx, skill, parsed.Files, record, repository.FullSkillMetadataPatch(skill), mutation); err != nil {
		return nil, err
	}
	return toSkillResponse(skill, true), nil
}

func (s *SkillService) Update(userID int64, id string, req *SkillUpdateInput) (*SkillResponse, error) {
	return s.UpdateContext(context.Background(), userID, id, req)
}

func (s *SkillService) UpdateContext(ctx context.Context, userID int64, id string, req *SkillUpdateInput) (*SkillResponse, error) {
	if userID <= 0 {
		return nil, newSkillError(SkillErrorInvalid, "admin actor is required", nil)
	}
	patch, err := skillMetadataPatchFromUpdate(req)
	if err != nil {
		return nil, err
	}
	if req.EntryContent == nil {
		skill, _, err := s.skillRepo.UpdateMetadataGoverned(ctx, id, patch, repository.SkillGovernanceMutation{
			Action: "update", ActorType: "admin", ActorUserID: userID,
			Reason: "admin updated Skill metadata",
		})
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, newSkillError(SkillErrorNotFound, "Skill not found", err)
			}
			return nil, err
		}
		files, err := s.skillRepo.ListFiles(skill.ID)
		if err != nil {
			return nil, err
		}
		skill.Files = files
		return toSkillResponse(skill, true), nil
	}

	skill, err := s.skillRepo.Get(id, true)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, newSkillError(SkillErrorNotFound, "Skill not found", err)
		}
		return nil, err
	}
	patch.Apply(skill)
	if strings.TrimSpace(skill.Name) == "" {
		return nil, newSkillError(SkillErrorInvalid, "name is required", nil)
	}
	parsed, err := parseManualSkill(&SkillInput{
		ID:            skill.ID,
		Name:          skill.Name,
		Description:   skill.Description,
		EntryContent:  *req.EntryContent,
		Files:         req.Files,
		Enabled:       &skill.Enabled,
		MinGroupLevel: skill.MinGroupLevel,
	})
	if err != nil {
		return nil, newSkillError(SkillErrorInvalid, "invalid Skill package", err)
	}
	skill.Checksum = parsed.Checksum
	skill.PackageChecksum = parsed.PackageChecksum
	skill.SourcePath = &parsed.SourcePath
	skill.EntryPath = "SKILL.md"
	record := s.buildImportRecord("update", skill, parsed, parsed.Files, skillparser.ImportReport{}, &userID)
	mutation := repository.SkillGovernanceMutation{
		Action: "update", ActorType: "admin", ActorUserID: userID,
		Reason: "admin updated manual Skill package",
	}
	if err := s.persistSkillPackage(ctx, skill, parsed.Files, record, patch, mutation); err != nil {
		return nil, err
	}
	return toSkillResponse(skill, true), nil
}

func skillMetadataPatchFromUpdate(req *SkillUpdateInput) (repository.SkillMetadataPatch, error) {
	patch := repository.SkillMetadataPatch{Enabled: req.Enabled, MinGroupLevel: req.MinGroupLevel}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return repository.SkillMetadataPatch{}, newSkillError(SkillErrorInvalid, "name is required", nil)
		}
		patch.Name = &name
	}
	if req.Description != nil {
		description := strings.TrimSpace(*req.Description)
		patch.Description = &description
	}
	if req.MinGroupLevel != nil && *req.MinGroupLevel < 0 {
		return repository.SkillMetadataPatch{}, newSkillError(SkillErrorInvalid, "min_group_level must be >= 0", nil)
	}
	return patch, nil
}

func (s *SkillService) Delete(userID int64, id string) error {
	if userID <= 0 {
		return newSkillError(SkillErrorInvalid, "admin actor is required", nil)
	}
	s.packageMu.Lock()
	defer s.packageMu.Unlock()

	paths, err := s.skillRepo.FilePathsForSkill(id)
	if err != nil {
		return err
	}
	if _, err := s.skillRepo.DeleteGoverned(context.Background(), id, repository.SkillGovernanceMutation{
		Action: "delete", ActorType: "admin", ActorUserID: userID,
		Reason: "admin deleted Skill",
	}); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return newSkillError(SkillErrorNotFound, "Skill not found", err)
		}
		return err
	}
	markSkillPackageRootsForDelayedCleanup(paths, "")
	s.scheduleSkillPackageCleanup()
	return nil
}

func (s *SkillService) ListHistory(id string) ([]*model.GovernanceEvent, error) {
	if s == nil || s.governanceRepo == nil {
		return nil, fmt.Errorf("skill governance repository is not available")
	}
	return s.governanceRepo.List(context.Background(), "skill", normalizeSkillID(id), 50)
}

func (s *SkillService) Rollback(userID, eventID int64, reason string) (*SkillResponse, *model.GovernanceEvent, error) {
	if userID <= 0 || eventID <= 0 {
		return nil, nil, newSkillError(SkillErrorInvalid, "actor and governance event are required", nil)
	}
	s.packageMu.Lock()
	defer s.packageMu.Unlock()

	skillID, files, err := s.skillRepo.RollbackFiles(eventID)
	if err != nil {
		return nil, nil, classifySkillGovernanceError(err)
	}
	if err := validateRetainedSkillFiles(files); err != nil {
		return nil, nil, newSkillError(SkillErrorConflict, "retained Skill package is unavailable", err)
	}
	var oldPaths []string
	oldPaths, err = s.skillRepo.FilePathsForSkill(skillID)
	if err != nil {
		return nil, nil, err
	}
	restored, event, err := s.skillRepo.RollbackGoverned(context.Background(), eventID, repository.SkillGovernanceMutation{
		Action: "rollback", ActorType: "admin", ActorUserID: userID,
		Reason: normalizeGovernanceReason(reason, "admin rolled back Skill change"),
	})
	if err != nil {
		return nil, nil, classifySkillGovernanceError(err)
	}
	keepRoot := ""
	if len(files) > 0 {
		keepRoot = skillPackageRootFromPath(files[0].StoragePath)
	}
	markSkillPackageRootsForDelayedCleanup(oldPaths, keepRoot)
	s.scheduleSkillPackageCleanup()
	if restored == nil {
		return nil, event, nil
	}
	return toSkillResponse(restored, true), event, nil
}

func (s *SkillService) ListFilesAdmin(id string) ([]SkillFileResponse, error) {
	if _, err := s.skillRepo.Get(id, true); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, newSkillError(SkillErrorNotFound, "Skill not found", err)
		}
		return nil, err
	}
	files, err := s.skillRepo.ListFiles(id)
	if err != nil {
		return nil, err
	}
	return toSkillFileResponses(files), nil
}

func (s *SkillService) ReadFileAdmin(id, path string) (string, *SkillFileResponse, error) {
	if _, err := s.skillRepo.Get(id, true); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return "", nil, newSkillError(SkillErrorNotFound, "Skill not found", err)
		}
		return "", nil, err
	}
	return s.readSkillFile(id, path)
}

func (s *SkillService) ListFilesForUser(userID int64, id string) ([]SkillFileResponse, error) {
	if _, err := s.visibleSkillForUser(userID, id); err != nil {
		return nil, err
	}
	files, err := s.skillRepo.ListFiles(id)
	if err != nil {
		return nil, err
	}
	return toSkillFileResponses(files), nil
}

func (s *SkillService) ReadFileForUser(userID int64, id, path string) (string, *SkillFileResponse, error) {
	if _, err := s.visibleSkillForUser(userID, id); err != nil {
		return "", nil, err
	}
	return s.readSkillFile(id, path)
}

func (s *SkillService) visibleSkillForUser(userID int64, id string) (*model.Skill, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, err
	}
	skill, err := s.skillRepo.Get(id, false)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, newSkillError(SkillErrorNotFound, "Skill not found", err)
		}
		return nil, err
	}
	groupLevel, err := s.groupLevelForUser(user)
	if err != nil {
		return nil, err
	}
	if !skillAllowedForUser(user, groupLevel, skill) {
		return nil, newSkillError(SkillErrorNotFound, "Skill not found", nil)
	}
	return skill, nil
}

func (s *SkillService) readSkillFile(id, path string) (string, *SkillFileResponse, error) {
	path = normalizeSkillFilePath(path)
	if path == "" {
		return "", nil, newSkillError(SkillErrorInvalid, "invalid Skill file path", nil)
	}
	file, err := s.skillRepo.GetFile(id, path)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return "", nil, newSkillError(SkillErrorNotFound, "Skill file not found", err)
		}
		return "", nil, err
	}
	content, err := readStoredSkillFile(file.StoragePath)
	if err != nil {
		return "", nil, err
	}
	resp := toSkillFileResponses([]model.SkillFile{*file})[0]
	return content, &resp, nil
}

func toSkillResponses(skills []*model.Skill, authorized bool) []*SkillResponse {
	resp := make([]*SkillResponse, 0, len(skills))
	for _, skill := range skills {
		item := toSkillResponse(skill, authorized)
		resp = append(resp, item)
	}
	return resp
}

func toSkillResponse(skill *model.Skill, authorized bool) *SkillResponse {
	resp := &SkillResponse{
		ID:              skill.ID,
		Name:            skill.Name,
		Description:     skill.Description,
		SourceType:      skill.SourceType,
		SourceURL:       skill.SourceURL,
		SourceRef:       skill.SourceRef,
		SourcePath:      skill.SourcePath,
		Checksum:        skill.Checksum,
		PackageChecksum: skill.PackageChecksum,
		EntryPath:       skill.EntryPath,
		MinGroupLevel:   skill.MinGroupLevel,
		Files:           toSkillFileResponses(skill.Files),
		Enabled:         skill.Enabled,
		IsBuiltin:       skill.IsBuiltin,
		Authorized:      authorized,
		CreatedBy:       skill.CreatedBy,
		CreatedAt:       skill.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       skill.UpdatedAt.Format(time.RFC3339),
	}
	return resp
}

func toSkillFileResponses(files []model.SkillFile) []SkillFileResponse {
	out := make([]SkillFileResponse, 0, len(files))
	for _, file := range files {
		out = append(out, SkillFileResponse{
			Path:     file.RelativePath,
			Kind:     file.Kind,
			Size:     file.Size,
			Checksum: file.Checksum,
		})
	}
	return out
}

func parseManualSkill(req *SkillInput) (skillparser.ParsedSkill, error) {
	entry := strings.TrimSpace(req.EntryContent)
	if entry == "" {
		return skillparser.ParsedSkill{}, fmt.Errorf("entry_content is required")
	}
	parsed, err := skillparser.ParseTextSkill("SKILL.md", []byte(entry))
	if err != nil {
		return skillparser.ParsedSkill{}, err
	}
	// 手动创建时，管理员在右侧文件包里添加的 reference 也属于显式选择。
	// 如果 SKILL.md 明确引用了某个相对路径但文件包里缺失，需要报错；反过来，
	// 管理员额外添加的 reference 可以保存，并通过 skill_list 暴露给 Agent 按需读取。
	provided := map[string][]byte{}
	for _, file := range req.Files {
		path := strings.TrimSpace(file.Path)
		if path == "" || strings.EqualFold(path, "SKILL.md") {
			continue
		}
		provided[filepath.ToSlash(path)] = []byte(file.Content)
	}
	files := append([]skillparser.ParsedSkillFile(nil), parsed.Files...)
	seen := map[string]struct{}{"SKILL.md": {}}
	for _, dep := range parsed.Dependencies {
		raw, ok := provided[dep]
		if !ok {
			return skillparser.ParsedSkill{}, fmt.Errorf("missing referenced skill file: %s", dep)
		}
		file, err := parseManualReference(dep, raw)
		if err != nil {
			return skillparser.ParsedSkill{}, err
		}
		files = append(files, file)
		seen[dep] = struct{}{}
	}
	for path, raw := range provided {
		if _, ok := seen[path]; !ok {
			file, err := parseManualReference(path, raw)
			if err != nil {
				return skillparser.ParsedSkill{}, err
			}
			files = append(files, file)
			seen[path] = struct{}{}
		}
	}
	parsed.Files = files
	parsed.PackageChecksum = skillparser.PackageChecksum(files)
	return parsed, nil
}

func parseManualReference(path string, raw []byte) (skillparser.ParsedSkillFile, error) {
	return skillparser.ParseReferenceFile(path, raw)
}

var skillUploadDir = filepolicy.SkillRoot

var skillPackageGracePeriod = 16 * time.Minute

// persistSkillPackage writes a content-addressed immutable root before the
// governed database update. The database becomes the package owner only after
// that update commits; failed roots remain unreferenced, while superseded roots
// enter delayed cleanup so in-flight readers can finish on the old version.
func (s *SkillService) persistSkillPackage(ctx context.Context, skill *model.Skill, parsedFiles []skillparser.ParsedSkillFile, record *model.SkillImportRecord, patch repository.SkillMetadataPatch, mutation repository.SkillGovernanceMutation) error {
	s.packageMu.Lock()
	defer s.packageMu.Unlock()

	oldPaths, err := s.skillRepo.FilePathsForSkill(skill.ID)
	if err != nil {
		return err
	}
	files, packageRoot, err := writeSkillPackage(skill.ID, skill.PackageChecksum, parsedFiles)
	if err != nil {
		s.scheduleSkillPackageCleanup()
		return err
	}
	if record != nil {
		record.SelectedFiles = marshalSelectedSkillFiles(parsedFiles)
		record.FileManifest = marshalSkillFileManifest(files)
	}
	if _, err := s.skillRepo.UpsertPackageGoverned(ctx, skill, files, record, patch, mutation); err != nil {
		s.scheduleSkillPackageCleanup()
		return err
	}
	markSkillPackageRootsForDelayedCleanup(oldPaths, packageRoot)
	s.scheduleSkillPackageCleanup()
	return nil
}

// persistSkillPackages prepares immutable package roots before opening the
// database transaction. The batch becomes active only after every package,
// import record, and governance event commits together; failed roots remain
// unreferenced and are reclaimed by the existing delayed cleanup.
func (s *SkillService) persistSkillPackages(packages []preparedSkillPackage) error {
	if len(packages) == 0 {
		return nil
	}
	s.packageMu.Lock()
	defer s.packageMu.Unlock()

	type preparedWrite struct {
		oldPaths    []string
		packageRoot string
		upsert      repository.GovernedSkillPackage
	}
	writes := make([]preparedWrite, 0, len(packages))
	for _, item := range packages {
		oldPaths, err := s.skillRepo.FilePathsForSkill(item.skill.ID)
		if err != nil {
			return err
		}
		files, packageRoot, err := writeSkillPackage(item.skill.ID, item.skill.PackageChecksum, item.parsedFiles)
		if err != nil {
			s.scheduleSkillPackageCleanup()
			return err
		}
		item.record.SelectedFiles = marshalSelectedSkillFiles(item.parsedFiles)
		item.record.FileManifest = marshalSkillFileManifest(files)
		writes = append(writes, preparedWrite{
			oldPaths: oldPaths, packageRoot: packageRoot,
			upsert: repository.GovernedSkillPackage{
				Skill: item.skill, Files: files, Record: item.record, Patch: item.patch, Mutation: item.mutation,
			},
		})
	}
	upserts := make([]repository.GovernedSkillPackage, 0, len(writes))
	for _, write := range writes {
		upserts = append(upserts, write.upsert)
	}
	if err := s.skillRepo.UpsertPackagesGoverned(context.Background(), upserts); err != nil {
		s.scheduleSkillPackageCleanup()
		return err
	}
	for _, write := range writes {
		markSkillPackageRootsForDelayedCleanup(write.oldPaths, write.packageRoot)
	}
	s.scheduleSkillPackageCleanup()
	return nil
}

func validateRetainedSkillFiles(files []model.SkillImportRecordFile) error {
	for _, file := range files {
		content, err := readStoredSkillFile(file.StoragePath)
		if err != nil {
			return fmt.Errorf("read retained Skill file %s: %w", file.RelativePath, err)
		}
		data := []byte(content)
		sum := sha256.Sum256(data)
		if int64(len(data)) != file.Size || hex.EncodeToString(sum[:]) != file.Checksum {
			return fmt.Errorf("retained Skill file %s no longer matches its manifest", file.RelativePath)
		}
	}
	return nil
}

func classifySkillGovernanceError(err error) error {
	if errors.Is(err, repository.ErrNotFound) {
		return newSkillError(SkillErrorNotFound, "governance event or Skill not found", err)
	}
	if errors.Is(err, repository.ErrGovernanceConflict) {
		return newSkillError(SkillErrorConflict, "Skill changed after this event or the event was already rolled back", err)
	}
	return err
}

func writeSkillPackage(skillID, packageChecksum string, parsedFiles []skillparser.ParsedSkillFile) ([]model.SkillFile, string, error) {
	if packageChecksum == "" {
		return nil, "", fmt.Errorf("package checksum is required")
	}
	if len(parsedFiles) == 0 {
		return nil, "", fmt.Errorf("skill package must contain SKILL.md")
	}
	root := filepath.Join(skillUploadDir, normalizeSkillID(skillID), packageChecksum)
	if files, complete := existingSkillPackageFiles(root, parsedFiles); complete {
		return files, root, nil
	}
	if err := os.RemoveAll(root); err != nil {
		return nil, "", err
	}
	files := make([]model.SkillFile, 0, len(parsedFiles))
	for _, parsed := range parsedFiles {
		rel, err := packageRelativePath(parsedFiles[0].Path, parsed.Path)
		if err != nil {
			return nil, root, err
		}
		target := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, root, err
		}
		if err := os.WriteFile(target, []byte(parsed.Content), 0o600); err != nil {
			return nil, root, err
		}
		files = append(files, model.SkillFile{
			RelativePath: rel,
			StoragePath:  target,
			Kind:         parsed.Kind,
			Size:         int64(len([]byte(parsed.Content))),
			Checksum:     parsed.Checksum,
		})
	}
	return files, root, nil
}

func existingSkillPackageFiles(root string, parsedFiles []skillparser.ParsedSkillFile) ([]model.SkillFile, bool) {
	files := make([]model.SkillFile, 0, len(parsedFiles))
	for _, parsed := range parsedFiles {
		rel, err := packageRelativePath(parsedFiles[0].Path, parsed.Path)
		if err != nil {
			return nil, false
		}
		target := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(target)
		if err != nil || string(data) != parsed.Content {
			return nil, false
		}
		files = append(files, model.SkillFile{
			RelativePath: rel,
			StoragePath:  target,
			Kind:         parsed.Kind,
			Size:         int64(len([]byte(parsed.Content))),
			Checksum:     parsed.Checksum,
		})
	}
	return files, true
}

func normalizeSkillFilePath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		path = "SKILL.md"
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return ""
	}
	return clean
}

func readStoredSkillFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("skill file path is empty")
	}
	root, err := filepath.Abs(skillUploadDir)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("skill file path is outside managed skill storage")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func packageRelativePath(entrySourcePath, sourcePath string) (string, error) {
	entrySourcePath = filepath.ToSlash(entrySourcePath)
	sourcePath = filepath.ToSlash(sourcePath)
	if sourcePath == entrySourcePath {
		return "SKILL.md", nil
	}
	base := filepath.Dir(entrySourcePath)
	rel, err := filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(sourcePath))
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || rel == ".." || rel == "." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid skill file path: %s", sourcePath)
	}
	return rel, nil
}

func markSkillPackageRootsForDelayedCleanup(paths []string, keepRoot string) {
	seen := map[string]struct{}{}
	for _, path := range paths {
		root := skillPackageRootFromPath(path)
		if root == "" || root == keepRoot {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		_ = os.Chtimes(root, time.Now(), time.Now())
	}
}

func (s *SkillService) cleanupExpiredSkillPackages() {
	s.packageMu.Lock()
	defer s.packageMu.Unlock()
	if s.skillRepo == nil {
		return
	}

	retainedPaths, err := s.skillRepo.RetainedFilePaths()
	if err != nil {
		return
	}
	activeRoots := map[string]struct{}{}
	for _, path := range retainedPaths {
		if root := skillPackageRootFromPath(path); root != "" {
			activeRoots[root] = struct{}{}
		}
	}
	cleanupExpiredSkillPackageRoots(skillUploadDir, activeRoots, time.Now())
}

// scheduleSkillPackageCleanup gives every package mutation a new generation.
// A stale timer cannot sweep roots after a newer mutation has changed the
// database owner; the eventual sweep re-reads active roots before deletion.
func (s *SkillService) scheduleSkillPackageCleanup() {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	s.cleanupGeneration++
	generation := s.cleanupGeneration
	if s.cleanupTimer != nil {
		s.cleanupTimer.Stop()
	}
	s.cleanupTimer = time.AfterFunc(skillPackageGracePeriod, func() {
		s.runScheduledSkillPackageCleanup(generation)
	})
}

func (s *SkillService) runScheduledSkillPackageCleanup(generation uint64) {
	s.cleanupMu.Lock()
	if generation != s.cleanupGeneration {
		s.cleanupMu.Unlock()
		return
	}
	s.cleanupTimer = nil
	s.cleanupMu.Unlock()
	s.cleanupExpiredSkillPackages()
}

func cleanupExpiredSkillPackageRoots(rootDir string, activeRoots map[string]struct{}, now time.Time) {
	skillIDs, err := os.ReadDir(rootDir)
	if err != nil {
		return
	}
	for _, skillID := range skillIDs {
		if !skillID.IsDir() {
			continue
		}
		packages, err := os.ReadDir(filepath.Join(rootDir, skillID.Name()))
		if err != nil {
			continue
		}
		for _, pkg := range packages {
			if !pkg.IsDir() {
				continue
			}
			root := filepath.Join(rootDir, skillID.Name(), pkg.Name())
			if _, active := activeRoots[root]; active {
				continue
			}
			info, err := pkg.Info()
			if err == nil && now.Sub(info.ModTime()) >= skillPackageGracePeriod {
				_ = os.RemoveAll(root)
			}
		}
	}
}

func skillPackageRootFromPath(path string) string {
	rel, err := filepath.Rel(skillUploadDir, path)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." || filepath.IsAbs(rel) {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return ""
	}
	return filepath.Join(skillUploadDir, parts[0], parts[1])
}

func skillAllowedForUser(user *model.User, groupLevel int, skill *model.Skill) bool {
	if user != nil && user.Role == "admin" {
		return skill.Enabled
	}
	return skill.Enabled && skill.MinGroupLevel <= groupLevel
}
