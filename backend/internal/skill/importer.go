package skill

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxArchiveBytes      int64 = 50 << 20
	MaxSingleSkillBytes        = 512 << 10
	MaxTotalSkillBytes         = 20 << 20
	MaxScannedFiles            = 5000
	MaxImportedSkills          = 500
	MaxSkillContentChars       = 120000
)

type ParsedSkill struct {
	ID              string
	Name            string
	Description     string
	Content         string
	SourcePath      string
	Checksum        string
	PackageChecksum string
	Files           []ParsedSkillFile
	AvailableFiles  []ParsedSkillFile
	Dependencies    []string
}

type ParsedSkillFile struct {
	Path            string
	Content         string
	Kind            string
	Size            int64
	Checksum        string
	Reason          string
	SelectedDefault bool
}

type ImportReport struct {
	Imported int      `json:"imported"`
	Skipped  []string `json:"skipped,omitempty"`
	Deduped  []string `json:"deduped,omitempty"`
	Details  []string `json:"details,omitempty"`
}

func ScanDir(root string) ([]ParsedSkill, ImportReport, error) {
	return ScanDirWithSelection(root, nil, nil)
}

func ScanDirWithSelection(root string, externalAvailablePaths []string, selectedFiles map[string][]string) ([]ParsedSkill, ImportReport, error) {
	var candidates []string
	var availableTextPaths []string
	var report ImportReport
	scanned := 0
	totalBytes := 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			report.Skipped = append(report.Skipped, "读取失败: "+path)
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if shouldSkipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if scanned >= MaxScannedFiles {
			report.Skipped = append(report.Skipped, rel+": 超过扫描数量限制")
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			report.Skipped = append(report.Skipped, rel+": 无法读取文件信息")
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			report.Skipped = append(report.Skipped, rel+": 跳过符号链接")
			return nil
		}
		if info.Size() <= 0 || info.Size() > MaxSingleSkillBytes {
			report.Skipped = append(report.Skipped, rel+": 文件为空或超过大小限制")
			return nil
		}
		if safeSkillRelativePath(rel) {
			availableTextPaths = append(availableTextPaths, rel)
		}
		if !isCandidatePath(rel) {
			return nil
		}
		if totalBytes+int(info.Size()) > MaxTotalSkillBytes {
			report.Skipped = append(report.Skipped, rel+": 导入内容总量超过限制")
			return nil
		}
		scanned++
		totalBytes += int(info.Size())
		candidates = append(candidates, rel)
		return nil
	})
	if err != nil {
		return nil, report, err
	}
	availableTextPaths = mergeTextPaths(availableTextPaths, externalAvailablePaths)
	sort.Strings(candidates)
	report.Details = append(report.Details, fmt.Sprintf("发现候选文件 %d 个，总候选正文约 %d bytes", len(candidates), totalBytes))

	var skills []ParsedSkill
	for _, rel := range candidates {
		if len(skills) >= MaxImportedSkills {
			report.Skipped = append(report.Skipped, rel+": 超过单次导入数量限制")
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			report.Skipped = append(report.Skipped, rel+": 读取失败")
			continue
		}
		parsed, err := ParseTextSkill(rel, data)
		if err != nil {
			report.Skipped = append(report.Skipped, rel+": "+err.Error())
			continue
		}
		files, available, fileReport := collectSkillFiles(parsed, availableTextPaths, selectedSkillFiles(parsed.SourcePath, selectedFiles), func(path string) ([]byte, bool, error) {
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if os.IsNotExist(err) {
				return nil, false, nil
			}
			return raw, err == nil, err
		})
		report.Skipped = append(report.Skipped, fileReport...)
		parsed.Files = files
		parsed.AvailableFiles = available
		parsed.PackageChecksum = PackageChecksum(files)
		skills = append(skills, parsed)
	}
	report.Imported = len(skills)
	return skills, report, nil
}

func mergeTextPaths(values ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, list := range values {
		for _, path := range list {
			path = filepath.ToSlash(strings.TrimSpace(path))
			if path == "" || !safeSkillRelativePath(path) {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func ImportZip(data []byte) ([]ParsedSkill, ImportReport, error) {
	return ImportZipSelected(data, nil)
}

func ImportZipSelected(data []byte, selectedPaths []string) ([]ParsedSkill, ImportReport, error) {
	return ImportZipSelectedFiles(data, selectedPaths, nil)
}

func ImportZipSelectedFiles(data []byte, selectedPaths []string, selectedFiles map[string][]string) ([]ParsedSkill, ImportReport, error) {
	var report ImportReport
	if int64(len(data)) > MaxArchiveBytes {
		return nil, report, fmt.Errorf("archive exceeds %d bytes", MaxArchiveBytes)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, report, fmt.Errorf("invalid zip archive: %w", err)
	}
	var files []*zip.File
	entryByPath := map[string]*zip.File{}
	var availableTextPaths []string
	totalBytes := 0
	for _, f := range zr.File {
		rel := filepath.ToSlash(f.Name)
		if !safeArchivePath(rel) {
			report.Skipped = append(report.Skipped, rel+": 非法路径")
			continue
		}
		if f.FileInfo().IsDir() || shouldSkipDirPath(rel) {
			continue
		}
		if len(files) >= MaxScannedFiles {
			report.Skipped = append(report.Skipped, rel+": 超过扫描数量限制")
			continue
		}
		if f.UncompressedSize64 == 0 || f.UncompressedSize64 > uint64(MaxSingleSkillBytes) {
			report.Skipped = append(report.Skipped, rel+": 文件为空或超过大小限制")
			continue
		}
		if isCandidatePath(rel) || safeSkillRelativePath(rel) {
			entryByPath[rel] = f
		}
		if safeSkillRelativePath(rel) {
			availableTextPaths = append(availableTextPaths, rel)
		}
		if !isCandidatePath(rel) {
			continue
		}
		if totalBytes+int(f.UncompressedSize64) > MaxTotalSkillBytes {
			report.Skipped = append(report.Skipped, rel+": 导入内容总量超过限制")
			continue
		}
		totalBytes += int(f.UncompressedSize64)
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	sort.Strings(availableTextPaths)
	report.Details = append(report.Details, fmt.Sprintf("Zip 候选文件 %d 个，总候选正文约 %d bytes", len(files), totalBytes))

	var skills []ParsedSkill
	for _, f := range files {
		if len(skills) >= MaxImportedSkills {
			report.Skipped = append(report.Skipped, f.Name+": 超过单次导入数量限制")
			continue
		}
		raw, err := readZipTextFile(f)
		if err != nil {
			report.Skipped = append(report.Skipped, filepath.ToSlash(f.Name)+": "+err.Error())
			continue
		}
		parsed, err := ParseTextSkill(filepath.ToSlash(f.Name), raw)
		if err != nil {
			report.Skipped = append(report.Skipped, f.Name+": "+err.Error())
			continue
		}
		// Zip 中可能包含大量 Markdown/文本文件。入口文件必须是 SKILL.md；
		// 入口之外的文本只进入 preview 候选清单，最终是否打包完全由管理员勾选决定。
		// 这样既能支持纯文本结构的 Skill 仓库，也避免用“同包默认必选”制造隐式复杂度。
		files, available, fileReport := collectSkillFiles(parsed, availableTextPaths, selectedSkillFiles(parsed.SourcePath, selectedFiles), func(path string) ([]byte, bool, error) {
			dep, ok := entryByPath[path]
			if !ok {
				return nil, false, nil
			}
			raw, err := readZipTextFile(dep)
			return raw, err == nil, err
		})
		report.Skipped = append(report.Skipped, fileReport...)
		parsed.Files = files
		parsed.AvailableFiles = available
		parsed.PackageChecksum = PackageChecksum(files)
		skills = append(skills, parsed)
	}
	skills = FilterParsedBySourcePaths(skills, selectedPaths, &report)
	report.Imported = len(skills)
	return skills, report, nil
}

func readZipTextFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("打开失败")
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, int64(MaxSingleSkillBytes)+1))
	if err != nil || len(raw) > MaxSingleSkillBytes {
		return nil, fmt.Errorf("读取失败或超过大小限制")
	}
	return raw, nil
}

func FilterParsedBySourcePaths(parsed []ParsedSkill, selectedPaths []string, report *ImportReport) []ParsedSkill {
	if selectedPaths == nil {
		return parsed
	}
	selected := make(map[string]struct{}, len(selectedPaths))
	for _, path := range selectedPaths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		selected[path] = struct{}{}
	}
	if len(selected) == 0 {
		if report != nil {
			report.Details = append(report.Details, "未选择任何 Skill")
		}
		return nil
	}
	out := make([]ParsedSkill, 0, len(parsed))
	for _, item := range parsed {
		if _, ok := selected[item.SourcePath]; !ok {
			continue
		}
		out = append(out, item)
		delete(selected, item.SourcePath)
	}
	if report != nil {
		for path := range selected {
			report.Skipped = append(report.Skipped, path+": 未在候选中找到")
		}
		sort.Strings(report.Skipped)
	}
	return out
}

func ParseTextSkill(sourcePath string, raw []byte) (ParsedSkill, error) {
	if !utf8ish(raw) {
		return ParsedSkill{}, fmt.Errorf("不是文本文件")
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	frontmatter, body := splitFrontmatter(text)
	body = strings.TrimSpace(body)
	if body == "" {
		return ParsedSkill{}, fmt.Errorf("内容为空")
	}
	if len([]rune(text)) > MaxSkillContentChars {
		return ParsedSkill{}, fmt.Errorf("内容超过 %d 字符", MaxSkillContentChars)
	}

	name := strings.TrimSpace(frontmatter["name"])
	if name == "" {
		name = strings.TrimSpace(frontmatter["title"])
	}
	if name == "" {
		name = nameFromPath(sourcePath)
	}
	desc := strings.TrimSpace(frontmatter["description"])
	if desc == "" {
		desc = firstParagraph(body)
	}
	id := slug(name)
	if id == "" {
		id = slug(strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath)))
	}
	if id == "" {
		return ParsedSkill{}, fmt.Errorf("无法生成 skill id")
	}
	sum := sha256.Sum256([]byte(text))
	entry := ParsedSkillFile{
		Path:            filepath.ToSlash(sourcePath),
		Content:         text,
		Kind:            "entry",
		Size:            int64(len([]byte(text))),
		Checksum:        hex.EncodeToString(sum[:]),
		Reason:          "entry",
		SelectedDefault: true,
	}
	return ParsedSkill{
		ID:           id,
		Name:         name,
		Description:  limitRunes(desc, 500),
		Content:      text,
		SourcePath:   filepath.ToSlash(sourcePath),
		Checksum:     hex.EncodeToString(sum[:]),
		Files:        []ParsedSkillFile{entry},
		Dependencies: discoverDependencyPaths(filepath.ToSlash(sourcePath), text),
	}, nil
}

func collectSkillFiles(parsed ParsedSkill, availablePaths, selectedExtra []string, loader func(path string) ([]byte, bool, error)) ([]ParsedSkillFile, []ParsedSkillFile, []string) {
	files := markEntryFile(append([]ParsedSkillFile(nil), parsed.Files...))
	available := append([]ParsedSkillFile(nil), files...)
	var skipped []string
	seen := map[string]struct{}{}
	selectedBytes := totalParsedFileBytes(files)
	for _, file := range files {
		seen[file.Path] = struct{}{}
	}
	selectedSet := pathSet(selectedExtra)
	dependencySet := pathSet(parsed.Dependencies)
	for _, candidate := range discoverPackageCandidatePaths(parsed.SourcePath, availablePaths) {
		if _, ok := seen[candidate.Path]; ok {
			continue
		}
		if _, ok := dependencySet[candidate.Path]; ok {
			candidate.Reason = "explicit"
		}
		selected := false
		if _, ok := selectedSet[candidate.Path]; ok {
			selected = true
			delete(selectedSet, candidate.Path)
		}
		if selected {
			reason := "selected"
			file, ok := loadSelectedReference(candidate.Path, reason, candidate.SelectedDefault, loader, &skipped)
			if !ok {
				candidate.Reason = reason
				available = append(available, candidate)
				seen[candidate.Path] = struct{}{}
				continue
			}
			if !reserveSkillFileBytes(file, &selectedBytes, &skipped) {
				continue
			}
			files = append(files, file)
			available = append(available, file)
			seen[file.Path] = struct{}{}
			continue
		}
		available = append(available, candidate)
		seen[candidate.Path] = struct{}{}
	}
	for _, dep := range parsed.Dependencies {
		if _, ok := seen[dep]; ok {
			continue
		}
		candidate := ParsedSkillFile{
			Path:            dep,
			Kind:            "reference",
			Reason:          "explicit",
			SelectedDefault: false,
		}
		if _, ok := selectedSet[dep]; ok {
			delete(selectedSet, dep)
			file, ok := loadSelectedReference(dep, "selected", false, loader, &skipped)
			if ok && reserveSkillFileBytes(file, &selectedBytes, &skipped) {
				files = append(files, file)
				available = append(available, file)
			} else {
				candidate.Reason = "selected"
				available = append(available, candidate)
			}
			seen[dep] = struct{}{}
			continue
		}
		available = append(available, candidate)
		seen[dep] = struct{}{}
	}
	for extra := range selectedSet {
		if _, ok := seen[extra]; ok || !safeSkillRelativePath(extra) {
			continue
		}
		file, ok := loadSelectedReference(extra, "selected", false, loader, &skipped)
		if !ok {
			continue
		}
		if !reserveSkillFileBytes(file, &selectedBytes, &skipped) {
			continue
		}
		files = append(files, file)
		available = append(available, file)
		seen[file.Path] = struct{}{}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Kind != files[j].Kind {
			return files[i].Kind == "entry"
		}
		return files[i].Path < files[j].Path
	})
	sort.Slice(available, func(i, j int) bool {
		if available[i].Kind != available[j].Kind {
			return available[i].Kind == "entry"
		}
		return available[i].Path < available[j].Path
	})
	return files, available, skipped
}

func totalParsedFileBytes(files []ParsedSkillFile) int64 {
	var total int64
	for _, file := range files {
		total += file.Size
	}
	return total
}

func reserveSkillFileBytes(file ParsedSkillFile, total *int64, skipped *[]string) bool {
	if *total+file.Size > MaxTotalSkillBytes {
		*skipped = append(*skipped, file.Path+": 导入内容总量超过限制")
		return false
	}
	*total += file.Size
	return true
}

func markEntryFile(files []ParsedSkillFile) []ParsedSkillFile {
	for i := range files {
		if files[i].Kind == "entry" {
			files[i].Reason = "entry"
			files[i].SelectedDefault = true
		}
	}
	return files
}

func loadSelectedReference(path, reason string, selectedDefault bool, loader func(path string) ([]byte, bool, error), skipped *[]string) (ParsedSkillFile, bool) {
	raw, ok, err := loader(path)
	if err != nil {
		*skipped = append(*skipped, path+": 读取依赖失败")
		return ParsedSkillFile{}, false
	}
	if !ok {
		*skipped = append(*skipped, path+": SKILL.md 引用或选中的依赖文件不存在")
		return ParsedSkillFile{}, false
	}
	file, err := ParseReferenceFile(path, raw)
	if err != nil {
		*skipped = append(*skipped, path+": "+err.Error())
		return ParsedSkillFile{}, false
	}
	file.Reason = reason
	file.SelectedDefault = selectedDefault
	return file, true
}

func discoverPackageCandidatePaths(entryPath string, availablePaths []string) []ParsedSkillFile {
	entryDir := filepath.Dir(filepath.ToSlash(entryPath))
	if entryDir == "." {
		entryDir = ""
	}
	var out []ParsedSkillFile
	seen := map[string]struct{}{}
	for _, path := range availablePaths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" || path == filepath.ToSlash(entryPath) || !safeSkillRelativePath(path) || isCandidatePath(path) {
			continue
		}
		rel := path
		if entryDir != "" {
			if !strings.HasPrefix(path, entryDir+"/") {
				continue
			}
			rel = strings.TrimPrefix(path, entryDir+"/")
		}
		if rel == "" || strings.HasPrefix(rel, "../") || rel == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, ParsedSkillFile{
			Path:            path,
			Kind:            "reference",
			Reason:          "candidate",
			SelectedDefault: false,
		})
		if len(out) >= 100 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SelectedDefault != out[j].SelectedDefault {
			return out[i].SelectedDefault
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func pathSet(paths []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		out[path] = struct{}{}
	}
	return out
}

func selectedSkillFiles(sourcePath string, selected map[string][]string) []string {
	if len(selected) == 0 {
		return nil
	}
	sourcePath = filepath.ToSlash(strings.TrimSpace(sourcePath))
	raw, ok := selected[sourcePath]
	if !ok {
		raw = selected[packageEntryPath(sourcePath)]
	}
	if len(raw) == 0 {
		return nil
	}
	entryDir := filepath.Dir(sourcePath)
	if entryDir == "." {
		entryDir = ""
	}
	out := make([]string, 0, len(raw))
	for _, path := range raw {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" || path == "SKILL.md" || path == sourcePath {
			continue
		}
		if safeSkillRelativePath(path) && entryDir != "" && !strings.HasPrefix(path, entryDir+"/") {
			path = filepath.ToSlash(filepath.Join(entryDir, path))
		}
		path = filepath.ToSlash(filepath.Clean(path))
		if safeSkillRelativePath(path) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func packageEntryPath(sourcePath string) string {
	if strings.EqualFold(filepath.Base(sourcePath), "SKILL.md") {
		return "SKILL.md"
	}
	return sourcePath
}

func ParseReferenceFile(path string, raw []byte) (ParsedSkillFile, error) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if !safeSkillRelativePath(path) {
		return ParsedSkillFile{}, fmt.Errorf("非法依赖路径")
	}
	if isCandidatePath(path) {
		return ParsedSkillFile{}, fmt.Errorf("依赖文件不能是另一个 SKILL.md")
	}
	if !utf8ish(raw) {
		return ParsedSkillFile{}, fmt.Errorf("不是文本文件")
	}
	text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n"))
	if text == "" {
		return ParsedSkillFile{}, fmt.Errorf("内容为空")
	}
	if len([]byte(text)) > MaxSingleSkillBytes {
		return ParsedSkillFile{}, fmt.Errorf("文件超过大小限制")
	}
	sum := sha256.Sum256([]byte(text))
	return ParsedSkillFile{
		Path:     path,
		Content:  text,
		Kind:     "reference",
		Size:     int64(len([]byte(text))),
		Checksum: hex.EncodeToString(sum[:]),
	}, nil
}

func PackageChecksum(files []ParsedSkillFile) string {
	h := sha256.New()
	sorted := append([]ParsedSkillFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	for _, file := range sorted {
		_, _ = h.Write([]byte(file.Path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(file.Checksum))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

var dependencyPathPattern = regexp.MustCompile(`(?i)(?:^|[\s("'` + "`" + `])([A-Za-z0-9][A-Za-z0-9._/-]*\.(?:md|txt|csv|tsv|json|yaml|yml))`)

func discoverDependencyPaths(entryPath, content string) []string {
	entryDir := filepath.Dir(filepath.ToSlash(entryPath))
	if entryDir == "." {
		entryDir = ""
	}
	seen := map[string]struct{}{}
	var out []string
	for _, match := range dependencyPathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 2 {
			continue
		}
		raw := filepath.ToSlash(strings.TrimSpace(match[1]))
		if raw == "" || strings.Contains(raw, "://") {
			continue
		}
		dep := raw
		if entryDir != "" && !strings.HasPrefix(dep, entryDir+"/") {
			dep = filepath.ToSlash(filepath.Join(entryDir, dep))
		}
		dep = filepath.ToSlash(filepath.Clean(dep))
		if !safeSkillRelativePath(dep) || isCandidatePath(dep) {
			continue
		}
		if _, ok := seen[dep]; ok {
			continue
		}
		seen[dep] = struct{}{}
		out = append(out, dep)
	}
	sort.Strings(out)
	return out
}

func safeSkillRelativePath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if !safeArchivePath(path) || strings.HasPrefix(filepath.Base(path), ".") || shouldSkipDirPath(path) {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".txt", ".csv", ".tsv", ".json", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func splitFrontmatter(text string) (map[string]string, string) {
	out := map[string]string{}
	if !strings.HasPrefix(text, "---\n") {
		return out, text
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return out, text
	}
	block := text[4 : 4+end]
	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key != "name" && key != "title" && key != "description" {
			continue
		}
		out[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return out, text[4+end+6:]
}

func shouldSkipDir(rel string) bool {
	base := filepath.Base(rel)
	switch base {
	case ".git", "node_modules", "vendor", "dist", "build", ".venv", "__pycache__":
		return true
	}
	if strings.HasPrefix(base, ".") && !strings.HasPrefix(filepath.ToSlash(rel), ".cursor") {
		return true
	}
	return false
}

func shouldSkipDirPath(path string) bool {
	parts := strings.Split(path, "/")
	for i := range parts[:max(0, len(parts)-1)] {
		part := parts[i]
		if part == ".cursor" {
			continue
		}
		if shouldSkipDir(strings.Join(parts[:i+1], "/")) {
			return true
		}
	}
	return false
}

func isCandidatePath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "skill.md"
}

func safeArchivePath(path string) bool {
	if path == "" || strings.Contains(path, "\\") || strings.HasPrefix(path, "/") {
		return false
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return false
	}
	for _, r := range path {
		if r == 0 || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func utf8ish(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	if !utf8.Valid(raw) {
		return false
	}
	zeros := 0
	for _, b := range raw {
		if b == 0 {
			zeros++
		}
	}
	return zeros == 0
}

func nameFromPath(path string) string {
	path = strings.Trim(path, "/")
	base := filepath.Base(path)
	if strings.EqualFold(base, "SKILL.md") {
		parent := filepath.Base(filepath.Dir(path))
		if parent != "." && parent != "" {
			return parent
		}
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func firstParagraph(body string) string {
	for _, part := range strings.Split(body, "\n\n") {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "#") {
			continue
		}
		return strings.Join(strings.Fields(part), " ")
	}
	return ""
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9_-]+`)
var slugDashes = regexp.MustCompile(`-+`)

func slug(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	var b strings.Builder
	for _, r := range input {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		case unicode.IsSpace(r), r == '/', r == '.', r == ':':
			b.WriteRune('-')
		}
	}
	out := slugUnsafe.ReplaceAllString(b.String(), "-")
	out = slugDashes.ReplaceAllString(out, "-")
	return strings.Trim(out, "-_")
}

func limitRunes(s string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes])
}
