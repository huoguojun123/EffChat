package agent

import (
	"sort"
	"strings"

	"github.com/huoguojun123/effchat/internal/modelbank"
	"github.com/huoguojun123/effchat/internal/repository"
)

const (
	maxPromptSkillItems = 24
	maxPromptSkillFiles = 16
	maxPromptSkillChars = 8000
)

// buildInstruction 生成本轮真正交给 Eino ChatModelAgent 的系统指令。
//
// 拼装顺序很重要：
//  1. 先渲染管理员配置的底层提示词模板，把日期、用户信息、会话信息、模型能力等
//     运行时变量填进去；
//  2. 补齐当前前端真实支持、但旧数据库模板可能缺失的工作区格式能力；
//  3. 再根据搜索决策追加联网搜索策略，告诉模型本轮是关闭、自适应还是始终搜索；
//  4. 再追加当前会话启用的结构化 Skills 清单。Skill 正文不会直接注入，而是通过
//     skill_list / skill_read / skill_search 按需读取；
//  5. 最后在启用 memory 或应用层搜索工具时追加工具调用预算，防止长工具链把 ADK
//     MaxIterations 耗尽后直接报错。
//
// 这个函数只负责“提示词拼装”，不直接决定工具是否挂载；真实工具挂载在 StreamChat
// 中根据 memory/search 决策完成。这样可以避免“提示词说有工具但后端没挂载”的错位。
func buildInstruction(configRepo *repository.ConfigRepository, req *ChatRequest, searchDecision modelbank.SearchDecision, mountedTools map[string]bool) (string, error) {
	templateText := ""
	if req != nil {
		templateText = strings.TrimSpace(req.RuntimePromptTemplate)
	}
	if templateText == "" {
		var err error
		templateText, err = loadPromptTemplate(configRepo)
		if err != nil {
			return "", err
		}
	}
	baseInstruction, err := renderPromptTemplate(templateText, buildPromptTemplateData(req))
	if err != nil {
		return "", err
	}
	baseInstruction = filterCapabilitySections(baseInstruction, mountedTools)
	baseInstruction = appendWorkspaceOutputInstruction(baseInstruction)
	instruction := buildSearchInstruction(baseInstruction, searchDecision, mountedTools)
	instruction = appendAvailableCapabilities(instruction, searchDecision, mountedTools)
	if hasMountedToolPrefix(mountedTools, "skill_") {
		instruction = appendSkillWorkspaceInstruction(instruction, req.EnabledSkills)
	}
	if len(enabledToolNames(mountedTools)) > 0 {
		instruction = appendToolBudgetInstruction(instruction, defaultToolRoundLimit, defaultToolCallLimit)
	}
	return instruction, nil
}

func appendWorkspaceOutputInstruction(instruction string) string {
	if strings.Contains(instruction, repository.MindMapOutputInstruction) {
		return instruction
	}
	return strings.TrimSpace(instruction + "\n\n## Workspace Mind Maps\n" + repository.MindMapOutputInstruction)
}

// appendMemoryInstruction 把当前会话记忆作为一段上下文追加到系统指令末尾。
// 工具契约由 memory ToolInfo 和默认模板的 Memory 段负责；这里仅注入动态正文，
// 避免同一套 add/replace/remove 规则在三处重复维护。
func appendMemoryInstruction(instruction, memory string) string {
	memory = strings.TrimSpace(memory)
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\n## Conversation Memory\n")
	if memory == "" {
		b.WriteString("Current saved memory is empty.")
	} else {
		b.WriteString("Current saved memory follows. Apply it naturally when relevant without narrating retrieval:\n\n")
		b.WriteString(memory)
	}
	return b.String()
}

func filterCapabilitySections(instruction string, mountedTools map[string]bool) string {
	filtered := instruction
	if !hasMountedToolPrefix(mountedTools, "file_") {
		filtered = removeMarkdownSection(filtered, "### File Tools")
	}
	if !hasMountedToolPrefix(mountedTools, "skill_") {
		filtered = removeMarkdownSection(filtered, "### Skill Tools")
	}
	if !mountedTools["web_search"] && !mountedTools["web_extract"] {
		filtered = removeMarkdownSection(filtered, "### Web Tools")
	}
	filtered = removeEmptyMarkdownSection(filtered, "## Tools")
	if !mountedTools["web_search"] && !mountedTools["web_extract"] {
		filtered = removeMarkdownSection(filtered, "## Session Web Evidence")
	}
	if !mountedTools["memory"] {
		filtered = removeMarkdownSection(filtered, "## Memory")
	}
	return strings.TrimSpace(filtered)
}

func removeEmptyMarkdownSection(markdown, target string) string {
	lines := strings.Split(markdown, "\n")
	targetLevel := markdownHeadingLevel(target)
	if targetLevel == 0 {
		return markdown
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != target {
			continue
		}
		for _, bodyLine := range lines[i+1:] {
			trimmed := strings.TrimSpace(bodyLine)
			level := markdownHeadingLevel(trimmed)
			if level > 0 && level <= targetLevel {
				return removeMarkdownSection(markdown, target)
			}
			if trimmed != "" {
				return markdown
			}
		}
		return removeMarkdownSection(markdown, target)
	}
	return markdown
}

func removeMarkdownSection(markdown, target string) string {
	lines := strings.Split(markdown, "\n")
	targetLevel := markdownHeadingLevel(target)
	if targetLevel == 0 {
		return markdown
	}
	out := make([]string, 0, len(lines))
	skipping := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !skipping && trimmed == target {
			skipping = true
			continue
		}
		if skipping {
			level := markdownHeadingLevel(trimmed)
			if level == 0 || level > targetLevel {
				continue
			}
			skipping = false
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func markdownHeadingLevel(line string) int {
	line = strings.TrimSpace(line)
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return 0
	}
	return level
}

func appendAvailableCapabilities(instruction string, searchDecision modelbank.SearchDecision, mountedTools map[string]bool) string {
	names := enabledToolNames(mountedTools)
	if len(names) == 0 && !searchDecision.UseModelNativeSearch {
		return instruction
	}
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\n## Available Capabilities This Turn\n")
	if searchDecision.UseModelNativeSearch {
		b.WriteString("- Model-native web search is enabled.\n")
	}
	if len(names) > 0 {
		b.WriteString("- Mounted tools: ")
		b.WriteString(strings.Join(names, ", "))
		b.WriteString(". Their ToolInfo schemas are the authoritative usage contract.\n")
	}
	return strings.TrimSpace(b.String())
}

func enabledToolNames(mountedTools map[string]bool) []string {
	names := make([]string, 0, len(mountedTools))
	for name, enabled := range mountedTools {
		if enabled && strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func hasMountedToolPrefix(mountedTools map[string]bool, prefix string) bool {
	for name, enabled := range mountedTools {
		if enabled && strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func appendSkillWorkspaceInstruction(instruction string, skills []SkillInstruction) string {
	if len(skills) == 0 {
		return instruction
	}
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\n## Enabled Structured Skills\n")
	b.WriteString("The following structured skills are enabled for this conversation. Their bodies are available through the mounted skill tools.\n")
	written := 0
	omitted := 0
	for _, skill := range skills {
		if written >= maxPromptSkillItems {
			omitted++
			continue
		}
		var line strings.Builder
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			name = strings.TrimSpace(skill.ID)
		}
		line.WriteString("- ")
		line.WriteString(truncateWebEvidence(name, 160))
		if id := strings.TrimSpace(skill.ID); id != "" && id != name {
			line.WriteString(" (`")
			line.WriteString(truncateWebEvidence(id, 120))
			line.WriteString("`)")
		}
		if desc := strings.TrimSpace(skill.Description); desc != "" {
			line.WriteString(": ")
			line.WriteString(truncateWebEvidence(desc, 400))
		}
		if len(skill.Files) > 0 {
			line.WriteString("; files: ")
			for i, file := range skill.Files {
				if i >= maxPromptSkillFiles {
					line.WriteString(", ...")
					break
				}
				if i > 0 {
					line.WriteString(", ")
				}
				line.WriteString(truncateWebEvidence(file.RelativePath, 180))
			}
		}
		line.WriteString("\n")
		if len([]rune(b.String()))+len([]rune(line.String())) > len([]rune(instruction))+maxPromptSkillChars {
			omitted++
			continue
		}
		b.WriteString(line.String())
		written++
	}
	if omitted > 0 {
		b.WriteString("- ")
		b.WriteString(intString(omitted))
		b.WriteString(" additional skill(s) omitted by the instruction context budget; use skill_list for the bounded runtime list.\n")
	}
	return b.String()
}
