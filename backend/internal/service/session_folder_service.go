package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

type SessionFolderService struct {
	folderRepo *repository.SessionFolderRepository
}

func NewSessionFolderService(folderRepo *repository.SessionFolderRepository) *SessionFolderService {
	return &SessionFolderService{folderRepo: folderRepo}
}

type CreateSessionFolderRequest struct {
	Name string `json:"name" binding:"required,max=80"`
}

type UpdateSessionFolderRequest struct {
	Name   *string `json:"name"`
	Pinned *bool   `json:"pinned"`
}

func (s *SessionFolderService) List(userID int64) ([]*model.SessionFolder, error) {
	return s.folderRepo.ListByUser(userID)
}

func (s *SessionFolderService) Create(userID int64, req *CreateSessionFolderRequest) (*model.SessionFolder, error) {
	name := normalizeFolderName(req.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrSessionFolderInvalid)
	}
	folder := &model.SessionFolder{
		UserID: userID,
		Name:   name,
	}
	if err := s.folderRepo.Create(folder); err != nil {
		return nil, err
	}
	return folder, nil
}

func (s *SessionFolderService) Update(id, userID int64, req *UpdateSessionFolderRequest) (*model.SessionFolder, error) {
	if req.Name == nil && req.Pinned == nil {
		return nil, fmt.Errorf("%w: at least one field is required", ErrSessionFolderInvalid)
	}
	patch := repository.SessionFolderPatch{}
	if req.Name != nil {
		name := normalizeFolderName(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: name is required", ErrSessionFolderInvalid)
		}
		patch.Name = name
		patch.NameSet = true
	}
	if req.Pinned != nil {
		patch.Pinned = *req.Pinned
		patch.PinnedSet = true
	}
	folder, err := s.folderRepo.Patch(id, userID, patch)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("%w: %v", ErrSessionFolderNotFound, err)
		}
		return nil, err
	}
	return folder, nil
}

func (s *SessionFolderService) Delete(id, userID int64) error {
	err := s.folderRepo.Delete(id, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrSessionFolderNotFound, err)
	}
	return err
}

func normalizeFolderName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}
