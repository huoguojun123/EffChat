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
	folder, err := s.folderRepo.GetByID(id, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("%w: %v", ErrSessionFolderNotFound, err)
		}
		return nil, err
	}
	if req.Name != nil {
		name := normalizeFolderName(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: name is required", ErrSessionFolderInvalid)
		}
		folder.Name = name
	}
	if req.Pinned != nil {
		if err := s.folderRepo.SetPinned(id, userID, *req.Pinned); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, fmt.Errorf("%w: %v", ErrSessionFolderNotFound, err)
			}
			return nil, err
		}
		return s.folderRepo.GetByID(id, userID)
	}
	if err := s.folderRepo.Update(folder); err != nil {
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
