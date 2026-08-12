package handler

import (
	"context"
	"fmt"

	"github.com/huoguojun123/EffChat/internal/repository"
)

type uploadLimits struct {
	MaxFileSizeMB   int      `json:"max_file_size_mb"`
	MaxSessionFiles int      `json:"max_session_files"`
	AllowedTypes    []string `json:"allowed_types"`
	PolicyDegraded  bool     `json:"policy_degraded,omitempty"`
}

const defaultDeploymentUploadMaxBytes int64 = 25 * 1024 * 1024

type attachmentProcessingPolicy struct {
	Enabled        bool
	TimeoutSeconds int
	MaxOutputMB    int
	Degraded       bool
}

func resolveUploadLimits(ctx context.Context, configRepo *repository.ConfigRepository, deploymentMaxBytes int64) (uploadLimits, error) {
	limits := uploadLimits{
		MaxFileSizeMB:   20,
		MaxSessionFiles: 50,
		AllowedTypes:    append([]string(nil), repository.DefaultUploadAllowedTypes...),
	}
	if configRepo != nil {
		var err error
		var degraded bool
		if limits.MaxFileSizeMB, degraded, err = configRepo.GetPolicyIntContext(ctx, "file_upload_max_size_mb", limits.MaxFileSizeMB); err != nil {
			return uploadLimits{}, err
		}
		limits.PolicyDegraded = limits.PolicyDegraded || degraded
		if limits.MaxSessionFiles, degraded, err = configRepo.GetPolicyIntContext(ctx, "file_upload_max_session_files", limits.MaxSessionFiles); err != nil {
			return uploadLimits{}, err
		}
		limits.PolicyDegraded = limits.PolicyDegraded || degraded
		if limits.AllowedTypes, degraded, err = configRepo.GetPolicyStringSliceContext(ctx, "file_upload_allowed_types", limits.AllowedTypes); err != nil {
			return uploadLimits{}, err
		}
		limits.PolicyDegraded = limits.PolicyDegraded || degraded
	}
	if limits.MaxFileSizeMB <= 0 {
		return uploadLimits{}, fmt.Errorf("file_upload_max_size_mb must be positive")
	}
	ceilingMB := uploadDeploymentCeilingMB(deploymentMaxBytes)
	if limits.MaxFileSizeMB > ceilingMB {
		// A legacy database value can predate the current deployment ceiling.
		// Enforce the reachable limit here without mutating that value; the Admin
		// read/write contract exposes the same effective ceiling for correction.
		limits.MaxFileSizeMB = ceilingMB
	}
	if limits.MaxSessionFiles <= 0 {
		return uploadLimits{}, fmt.Errorf("file_upload_max_session_files must be positive")
	}
	if len(limits.AllowedTypes) == 0 {
		return uploadLimits{}, fmt.Errorf("file_upload_allowed_types must not be empty")
	}
	limits.AllowedTypes = normalizeUploadAllowedTypes(limits.AllowedTypes)
	if len(limits.AllowedTypes) == 0 {
		return uploadLimits{}, fmt.Errorf("file_upload_allowed_types has no valid values")
	}
	return limits, nil
}

func uploadDeploymentCeilingMB(deploymentMaxBytes int64) int {
	bytes := defaultDeploymentUploadMaxBytes
	if deploymentMaxBytes > 0 {
		bytes = deploymentMaxBytes
	}
	ceiling := int(bytes >> 20)
	if ceiling < 1 {
		return 1
	}
	return ceiling
}

func resolveAttachmentProcessingPolicy(ctx context.Context, configRepo *repository.ConfigRepository) (attachmentProcessingPolicy, error) {
	policy := attachmentProcessingPolicy{Enabled: true, TimeoutSeconds: 60, MaxOutputMB: 5}
	if configRepo == nil {
		return policy, nil
	}
	var degraded bool
	var err error
	if policy.Enabled, degraded, err = configRepo.GetPolicyBoolContext(ctx, "attachment_extract_enabled", policy.Enabled); err != nil {
		return attachmentProcessingPolicy{}, err
	}
	policy.Degraded = policy.Degraded || degraded
	if policy.TimeoutSeconds, degraded, err = configRepo.GetPolicyIntContext(ctx, "attachment_extract_timeout_seconds", policy.TimeoutSeconds); err != nil {
		return attachmentProcessingPolicy{}, err
	}
	policy.Degraded = policy.Degraded || degraded
	if policy.MaxOutputMB, degraded, err = configRepo.GetPolicyIntContext(ctx, "attachment_max_output_mb", policy.MaxOutputMB); err != nil {
		return attachmentProcessingPolicy{}, err
	}
	policy.Degraded = policy.Degraded || degraded
	if policy.TimeoutSeconds <= 0 || policy.MaxOutputMB <= 0 {
		return attachmentProcessingPolicy{}, fmt.Errorf("attachment processing limits must be positive")
	}
	return policy, nil
}

func normalizeUploadAllowedTypes(allowedTypes []string) []string {
	normalized := make([]string, 0, len(allowedTypes)+3)
	for _, contentType := range allowedTypes {
		if contentType == "image/*" {
			normalized = append(normalized, "image/png", "image/jpeg", "image/gif", "image/webp")
			continue
		}
		normalized = append(normalized, contentType)
	}
	return normalized
}
