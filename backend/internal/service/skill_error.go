package service

import "fmt"

type SkillErrorKind string

const (
	SkillErrorInvalid           SkillErrorKind = "invalid"
	SkillErrorNotFound          SkillErrorKind = "not_found"
	SkillErrorNotAuthorized     SkillErrorKind = "not_authorized"
	SkillErrorConflict          SkillErrorKind = "conflict"
	SkillErrorSourceUnavailable SkillErrorKind = "source_unavailable"
	SkillErrorSessionNotFound   SkillErrorKind = "session_not_found"
)

// SkillError carries only a deliberately public message. Err preserves the
// operational cause for request-correlated logs without exposing repository,
// filesystem, Git, URL, or credential details to API clients.
type SkillError struct {
	Kind    SkillErrorKind
	Message string
	Err     error
}

func (e *SkillError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

func (e *SkillError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newSkillError(kind SkillErrorKind, message string, err error) error {
	return &SkillError{Kind: kind, Message: message, Err: err}
}
