package service

import "errors"

var (
	ErrSessionFolderInvalid  = errors.New("invalid session folder")
	ErrSessionFolderNotFound = errors.New("session folder not found")
)
