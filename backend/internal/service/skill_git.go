package service

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/netpolicy"
	skillparser "github.com/huoguojun123/EffChat/internal/skill"
)

var gitRefPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,120}$`)

var trustedGitHosts = map[string]struct{}{
	"github.com":    {},
	"gitlab.com":    {},
	"bitbucket.org": {},
	"codeberg.org":  {},
}

func listGitBranches(ctx context.Context, repoURL string) ([]string, string, error) {
	if err := validateGitURL(ctx, repoURL); err != nil {
		return nil, "", err
	}
	lsCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := gitRemoteCommand(lsCtx, "ls-remote", "--symref", repoURL, "HEAD", "refs/heads/*")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, "", fmt.Errorf("git branch scan failed: %s", strings.TrimSpace(string(out)))
	}
	return parseGitRemoteRefs(string(out)), parseGitDefaultRef(string(out)), nil
}

// scanGitRef 使用 partial clone + sparse checkout，而不是普通 clone。
//
// preview 阶段只拉 SKILL.md 的 blob，同时用 git ls-tree 拿全仓文件名清单，
// 让管理员看到可选 references；import 阶段再额外拉管理员手选的文本文件。
// 这条边界很重要：入口自动、references 手选，避免大仓库或同包目录被隐式整包下载。
func scanGitRef(ctx context.Context, repoURL, ref string, selectedFiles map[string][]string) ([]skillparser.ParsedSkill, skillparser.ImportReport, error) {
	if err := validateGitURL(ctx, repoURL); err != nil {
		return nil, skillparser.ImportReport{}, err
	}
	tmp, err := os.MkdirTemp("", "effchat-skill-import-*")
	if err != nil {
		return nil, skillparser.ImportReport{}, err
	}
	defer os.RemoveAll(tmp)

	cloneCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	args := []string{"clone", "--filter=blob:none", "--sparse", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, repoURL, tmp)
	cmd := gitRemoteCommand(cloneCtx, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, skillparser.ImportReport{}, fmt.Errorf("git clone failed: %s", strings.TrimSpace(string(out)))
	}
	treePaths, treeErr := listGitTreePaths(ctx, tmp)
	if treeErr != nil {
		return nil, skillparser.ImportReport{}, treeErr
	}
	sparseCtx, sparseCancel := context.WithTimeout(ctx, 20*time.Second)
	defer sparseCancel()
	patterns := skillSparseCheckoutPatterns()
	sparseArgs := append([]string{"-C", tmp, "sparse-checkout", "set", "--no-cone"}, patterns...)
	sparseCmd := exec.CommandContext(sparseCtx, "git", sparseArgs...)
	sparseCmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := sparseCmd.CombinedOutput(); err != nil {
		return nil, skillparser.ImportReport{}, fmt.Errorf("git sparse checkout failed: %s", strings.TrimSpace(string(out)))
	}
	parsed, _, err := skillparser.ScanDirWithSelection(tmp, treePaths, selectedFiles)
	if err != nil {
		return nil, skillparser.ImportReport{}, err
	}
	deps := selectedSkillFileSparseCheckoutPatterns(parsed)
	if len(deps) > 0 {
		fullPatterns := append(append([]string{}, patterns...), deps...)
		depCtx, depCancel := context.WithTimeout(ctx, 20*time.Second)
		defer depCancel()
		depArgs := append([]string{"-C", tmp, "sparse-checkout", "set", "--no-cone"}, fullPatterns...)
		depCmd := exec.CommandContext(depCtx, "git", depArgs...)
		depCmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := depCmd.CombinedOutput(); err != nil {
			return nil, skillparser.ImportReport{}, fmt.Errorf("git dependency checkout failed: %s", strings.TrimSpace(string(out)))
		}
	}
	parsed, report, err := skillparser.ScanDirWithSelection(tmp, treePaths, selectedFiles)
	report.Details = append([]string{fmt.Sprintf("Git sparse checkout ref=%s tree_files=%d entry_patterns=%d selected_file_patterns=%d", refLabel(ref), len(treePaths), len(patterns), len(deps))}, report.Details...)
	return parsed, report, err
}

func listGitTreePaths(ctx context.Context, repoDir string) ([]string, error) {
	treeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(treeCtx, "git", "-C", repoDir, "ls-tree", "-r", "--name-only", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git tree scan failed: %s", strings.TrimSpace(string(out)))
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func refLabel(ref string) string {
	if strings.TrimSpace(ref) == "" {
		return "default"
	}
	return ref
}

func skillSparseCheckoutPatterns() []string {
	return []string{
		"/SKILL.md",
		"/*/SKILL.md",
		"/*/*/SKILL.md",
		"/*/*/*/SKILL.md",
		"/*/*/*/*/SKILL.md",
		"/*/*/*/*/*/SKILL.md",
	}
}

func selectedSkillFileSparseCheckoutPatterns(parsed []skillparser.ParsedSkill) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range parsed {
		for _, file := range item.AvailableFiles {
			if file.Kind == "entry" || file.Reason != "selected" {
				continue
			}
			path := strings.TrimSpace(file.Path)
			if path == "" {
				continue
			}
			pattern := "/" + strings.TrimPrefix(path, "/")
			if _, ok := seen[pattern]; ok {
				continue
			}
			seen[pattern] = struct{}{}
			out = append(out, pattern)
		}
	}
	sort.Strings(out)
	return out
}

func parseGitDefaultRef(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ref: ") || !strings.HasSuffix(line, "\tHEAD") {
			continue
		}
		ref := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "ref: "), "\tHEAD"))
		if branch := strings.TrimPrefix(ref, "refs/heads/"); branch != ref {
			return branch
		}
	}
	return ""
}

func parseGitRemoteRefs(output string) []string {
	seen := map[string]struct{}{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		ref := fields[len(fields)-1]
		branch := strings.TrimPrefix(ref, "refs/heads/")
		if branch == ref || branch == "" || strings.Contains(branch, "^{}") {
			continue
		}
		if !safeGitRef(branch) {
			continue
		}
		seen[branch] = struct{}{}
	}
	branches := make([]string, 0, len(seen))
	for branch := range seen {
		branches = append(branches, branch)
	}
	sort.Strings(branches)
	return branches
}

func selectGitRef(requested, defaultRef string, branches []string) string {
	if requested != "" {
		return requested
	}
	if defaultRef != "" {
		return defaultRef
	}
	for _, candidate := range []string{"main", "master"} {
		for _, branch := range branches {
			if branch == candidate {
				return branch
			}
		}
	}
	if len(branches) > 0 {
		return branches[0]
	}
	return ""
}

func safeGitRef(ref string) bool {
	if strings.Contains(ref, "..") || strings.HasPrefix(ref, "-") || strings.HasSuffix(ref, ".lock") {
		return false
	}
	return gitRefPattern.MatchString(ref)
}

func gitRemoteCommand(ctx context.Context, args ...string) *exec.Cmd {
	commandArgs := append([]string{"-c", "http.followRedirects=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", commandArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
}

func validateGitURL(ctx context.Context, raw string) error {
	return validateGitURLWithResolver(ctx, net.DefaultResolver, raw)
}

func validateGitURLWithResolver(ctx context.Context, resolver netpolicy.IPResolver, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" {
		return fmt.Errorf("invalid git url")
	}
	if parsed.User != nil {
		return fmt.Errorf("invalid git url: url must not include credentials")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return fmt.Errorf("invalid git url: unsupported port")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("invalid git url: blocked host")
	}
	if ip := net.ParseIP(host); ip != nil && netpolicy.IsBlockedIP(ip) {
		return fmt.Errorf("invalid git url: blocked address")
	}
	if _, trusted := trustedGitHosts[host]; trusted {
		return nil
	}
	if err := netpolicy.ValidatePublicHTTPURL(ctx, resolver, raw); err != nil {
		return fmt.Errorf("invalid git url: %w", err)
	}
	return nil
}
