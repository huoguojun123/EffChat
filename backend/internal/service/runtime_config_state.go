package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/huoguojun123/effchat/internal/model"
)

const (
	RuntimeStateReady        = "ready"
	RuntimeStateDefault      = "default"
	RuntimeStateDisabled     = "disabled"
	RuntimeStateUnconfigured = "unconfigured"
	RuntimeStateUnavailable  = "unavailable"
)

type RuntimeConfigState struct {
	State   string `json:"state"`
	Cause   string `json:"cause,omitempty"`
	Version string `json:"version"`
}

type SearchRuntimeConfigState struct {
	Search  RuntimeConfigState `json:"search"`
	Extract RuntimeConfigState `json:"extract"`
}

func runtimeConfigState(state, cause, version string) RuntimeConfigState {
	return RuntimeConfigState{State: state, Cause: cause, Version: version}
}

func runtimeConfigVersion(domain string, parts []string) string {
	sort.Strings(parts)
	hash := sha256.Sum256([]byte(domain + "\x00" + strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(hash[:])
}

func toolConfigVersion(items []*model.ToolConfig) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s|%t|%d|%d|%d", item.Key, item.Enabled, item.TimeoutSeconds, item.SortOrder, item.UpdatedAt.UnixNano()))
	}
	return runtimeConfigVersion("tools", parts)
}

func externalServiceVersion(kind string, items []*model.ExternalService) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil || item.Kind != kind {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s|%t|%d|%d", item.Key, item.Enabled, item.SortOrder, item.UpdatedAt.UnixNano()))
	}
	return runtimeConfigVersion("external:"+kind, parts)
}
