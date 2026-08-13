package model

import (
	"encoding/json"
	"time"
)

// GovernanceEvent is an append-only Tool/Skill catalog mutation record.
// BeforeState and AfterState intentionally contain metadata only: Skill
// package contents remain in managed storage and are referenced through the
// associated import record.
type GovernanceEvent struct {
	ID                  int64           `json:"id"`
	ResourceType        string          `json:"resource_type"`
	ResourceKey         string          `json:"resource_key"`
	Action              string          `json:"action"`
	ActorType           string          `json:"actor_type"`
	ActorUserID         *int64          `json:"actor_user_id,omitempty"`
	Reason              string          `json:"reason"`
	BeforeState         json.RawMessage `json:"before_state,omitempty"`
	AfterState          json.RawMessage `json:"after_state,omitempty"`
	SkillImportRecordID *int64          `json:"skill_import_record_id,omitempty"`
	RollbackOfEventID   *int64          `json:"rollback_of_event_id,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
}

// SkillImportRecordFile keeps an immutable reference to one managed file in a
// package version. It does not duplicate the file body in PostgreSQL.
type SkillImportRecordFile struct {
	ImportRecordID int64  `json:"import_record_id"`
	RelativePath   string `json:"path"`
	StoragePath    string `json:"-"`
	Kind           string `json:"kind"`
	Size           int64  `json:"size"`
	Checksum       string `json:"checksum"`
}
