package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func (s *SkillService) UpdateSessionSkills(sessionID, userID int64, ids []string) ([]string, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return nil, err
	}
	skills, err := s.skillRepo.List(false)
	if err != nil {
		return nil, err
	}
	groupLevel, err := s.groupLevelForUser(user)
	if err != nil {
		return nil, err
	}
	allowed := allowedSkillSet(user, groupLevel, skills)
	clean := dedupeSkillIDs(ids)
	for _, id := range clean {
		if _, ok := allowed[id]; !ok {
			return nil, newSkillError(SkillErrorNotAuthorized, "Skill is not authorized", nil)
		}
	}
	if err := s.sessionRepo.UpdateEnabledSkills(sessionID, userID, clean); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, newSkillError(SkillErrorSessionNotFound, "session not found", err)
		}
		return nil, err
	}
	return clean, nil
}

// EnabledInstructionsForSession 是 Agent 运行前的最后一道服务端权限闸门。
//
// 前端只负责展示当前用户“看得到”的 Skill，但会话 metadata 仍可能残留旧 ID：
// 管理员调高 min_group_level、禁用 Skill，或用户被移动到更低分级组后，浏览器里的
// skills_enabled 不一定马上刷新。因此这里每轮运行都重新按用户角色、用户组等级、
// Skill 启停状态和会话勾选列表过滤，确保旧前端状态不能绕过权限。
func (s *SkillService) EnabledInstructionsForSessionContext(ctx context.Context, user *model.User, sessionMetadata []byte) ([]SkillInstruction, error) {
	if user == nil {
		return nil, nil
	}
	metadata := parseMetadata(sessionMetadata)
	rawIDs, _ := metadata["skills_enabled"].([]interface{})
	ids := make([]string, 0, len(rawIDs))
	for _, raw := range rawIDs {
		if v, ok := raw.(string); ok {
			ids = append(ids, v)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	skills, err := s.skillRepo.ListContext(ctx, false)
	if err != nil {
		return nil, err
	}
	groupLevel, err := s.groupLevelForUserContext(ctx, user)
	if err != nil {
		return nil, err
	}
	allowed := allowedSkillSet(user, groupLevel, skills)
	byID := make(map[string]*model.Skill, len(skills))
	for _, skill := range skills {
		byID[skill.ID] = skill
	}
	instructions := make([]SkillInstruction, 0, len(ids))
	for _, id := range dedupeSkillIDs(ids) {
		skill, ok := byID[id]
		if !ok {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		instructions = append(instructions, SkillInstruction{
			ID:          skill.ID,
			Name:        skill.Name,
			Description: skill.Description,
			Files:       skill.Files,
		})
	}
	return instructions, nil
}

func allowedSkillSet(user *model.User, groupLevel int, skills []*model.Skill) map[string]struct{} {
	out := map[string]struct{}{}
	if user.Role == "admin" {
		for _, skill := range skills {
			if skill.Enabled {
				out[skill.ID] = struct{}{}
			}
		}
		return out
	}
	for _, skill := range skills {
		if !skill.Enabled {
			continue
		}
		if skill.MinGroupLevel <= groupLevel {
			out[skill.ID] = struct{}{}
		}
	}
	return out
}

func (s *SkillService) groupLevelForUser(user *model.User) (int, error) {
	return s.groupLevelForUserContext(context.Background(), user)
}

func (s *SkillService) groupLevelForUserContext(ctx context.Context, user *model.User) (int, error) {
	if user == nil || user.Role == "admin" {
		return 0, nil
	}
	return s.userRepo.GetGroupLevelContext(ctx, user.ID)
}

func parseMetadata(raw []byte) map[string]interface{} {
	out := map[string]interface{}{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]interface{}{}
	}
	return out
}

func dedupeSkillIDs(ids []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = normalizeSkillID(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
