package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeMemoryStore 内存实现，按 sessionID 隔离。
type fakeMemoryStore struct {
	data   map[int64]string
	getErr error
	setErr error
}

type conflictingMemoryStore struct {
	*fakeMemoryStore
	replacement string
}

func (f *conflictingMemoryStore) CompareAndSetWithChange(_ context.Context, sessionID, _ int64, _, _, _, _, _ string, _ int) (bool, error) {
	f.data[sessionID] = f.replacement
	return false, nil
}

func (f *fakeMemoryStore) Get(sessionID int64) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.data[sessionID], nil
}

func (f *fakeMemoryStore) Set(sessionID int64, content string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.data[sessionID] = content
	return nil
}

func runMemory(t *testing.T, tool *MemoryTool, in MemoryInput) MemoryOutput {
	t.Helper()
	raw := runMemoryRaw(t, tool, in)
	var out MemoryOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	return out
}

func runMemoryRaw(t *testing.T, tool *MemoryTool, in MemoryInput) string {
	t.Helper()
	args, _ := json.Marshal(in)
	raw, err := tool.InvokableRun(context.Background(), string(args))
	if err != nil {
		t.Fatalf("InvokableRun returned Go error (should degrade gracefully): %v", err)
	}
	return raw
}

func TestMemoryTool_WriteThenRead(t *testing.T) {
	store := &fakeMemoryStore{data: map[int64]string{}}
	tool := NewMemoryTool(store, 42, 1)

	w := runMemory(t, tool, MemoryInput{Action: "write", Content: "用户偏好简洁中文回答"})
	if !w.OK || w.Error != "" || w.Action != "write" {
		t.Fatalf("write failed: %+v", w)
	}
	if !strings.Contains(store.data[42], "## Current Progress") || !strings.Contains(store.data[42], "用户偏好简洁中文回答") {
		t.Errorf("store not updated: %q", store.data[42])
	}

	r := runMemory(t, tool, MemoryInput{Action: "read"})
	if !r.OK || !strings.Contains(r.MemoryText, "用户偏好简洁中文回答") {
		t.Errorf("read memory_text = %+v", r)
	}
}

func TestMemoryTool_AddViewReplaceRemoveClear(t *testing.T) {
	store := &fakeMemoryStore{data: map[int64]string{}}
	tool := NewMemoryTool(store, 42, 1)

	first := runMemory(t, tool, MemoryInput{Action: "add", Section: "user_preferences", Content: "User prefers concise Chinese answers."})
	if first.Error != "" {
		t.Fatalf("add error: %s", first.Error)
	}
	if !first.OK || first.Action != "add" || first.LineNumber != 1 || first.ChangedItem != "User prefers concise Chinese answers." {
		t.Fatalf("add should return compact changed item, got %+v", first)
	}

	duplicate := runMemory(t, tool, MemoryInput{Action: "add", Section: "user_preferences", Content: "user prefers concise chinese answers."})
	if duplicate.Error != "" {
		t.Fatalf("duplicate add should be a no-op, got error: %s", duplicate.Error)
	}
	if !duplicate.OK || duplicate.LineNumber != 1 || duplicate.ChangedItem == "" {
		t.Fatalf("duplicate add should return existing item reference: %+v", duplicate)
	}

	second := runMemory(t, tool, MemoryInput{Action: "add", Section: "project_context", Content: "Project direction is self-hosted small-team workbench."})
	if !second.OK || second.LineNumber != 2 || second.Section != "project_context" {
		t.Fatalf("second add should return item #2, got %+v", second)
	}
	view := runMemory(t, tool, MemoryInput{Action: "view"})
	if !view.OK || len(view.Items) != 2 || view.Items[1].LineNumber != 2 || !strings.Contains(view.MemoryText, "2 [project_context]") {
		t.Fatalf("view should return numbered items, got %+v", view)
	}
	rawView := runMemoryRaw(t, tool, MemoryInput{Action: "view"})
	var viewFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawView), &viewFields); err != nil {
		t.Fatalf("unmarshal raw view: %v", err)
	}
	if _, ok := viewFields["content"]; ok {
		t.Fatalf("view should not return full memory content: %s", rawView)
	}
	if _, ok := viewFields["sections"]; ok {
		t.Fatalf("view should not return full memory sections: %s", rawView)
	}

	replaced := runMemory(t, tool, MemoryInput{Action: "replace", LineNumber: 2, Content: "Project direction is a lightweight self-hosted agent workbench."})
	if replaced.Error != "" {
		t.Fatalf("replace error: %s", replaced.Error)
	}
	if !replaced.OK || replaced.Action != "replace" || replaced.LineNumber != 2 || !strings.Contains(replaced.ChangedItem, "lightweight self-hosted agent workbench") {
		t.Fatalf("replace did not return compact changed item: %+v", replaced)
	}

	removed := runMemory(t, tool, MemoryInput{Action: "remove", LineNumber: 1})
	if removed.Error != "" {
		t.Fatalf("remove error: %s", removed.Error)
	}
	if !removed.OK || removed.Action != "remove" || removed.LineNumber != 1 || !strings.Contains(removed.ChangedItem, "concise Chinese") {
		t.Fatalf("remove did not return deleted item: %+v", removed)
	}
	preference := runMemory(t, tool, MemoryInput{Action: "add", Section: "user_preferences", Content: "Use English commit messages."})
	if preference.Error != "" || preference.Section != "user_preferences" || preference.ChangedItem != "Use English commit messages." {
		t.Fatalf("section add failed: %+v", preference)
	}

	cleared := runMemory(t, tool, MemoryInput{Action: "clear"})
	if !cleared.OK || cleared.Error != "" || cleared.Action != "clear" || store.data[42] != "" {
		t.Fatalf("clear failed: %+v store=%q", cleared, store.data[42])
	}
}

func TestMemoryTool_DoesNotOverwriteConcurrentMemoryChange(t *testing.T) {
	store := &conflictingMemoryStore{
		fakeMemoryStore: &fakeMemoryStore{data: map[int64]string{
			42: "## User Preferences\n- Prefer concise answers.",
		}},
		replacement: "## User Preferences\n- Prefer detailed answers.",
	}
	tool := NewMemoryTool(store, 42, 1)

	out := runMemory(t, tool, MemoryInput{
		Action:     "replace",
		LineNumber: 1,
		Content:    "Prefer short answers.",
	})
	if out.OK || !strings.Contains(out.Error, "view memory again") {
		t.Fatalf("expected retryable memory conflict, got %+v", out)
	}
	if got := store.data[42]; got != store.replacement {
		t.Fatalf("concurrent memory was overwritten: %q", got)
	}
}

func TestMemoryTool_RemoveByUniqueContentAndRejectsAmbiguousContent(t *testing.T) {
	store := &fakeMemoryStore{data: map[int64]string{
		42: strings.Join([]string{
			"User prefers terse status updates.",
			"User prefers terse final answers.",
			"Use English commit messages.",
		}, "\n"),
	}}
	tool := NewMemoryTool(store, 42, 1)

	ambiguous := runMemory(t, tool, MemoryInput{Action: "remove", Content: "terse"})
	if ambiguous.Error == "" || !strings.Contains(ambiguous.Error, "multiple") {
		t.Fatalf("expected ambiguous remove error, got %+v", ambiguous)
	}

	removed := runMemory(t, tool, MemoryInput{Action: "delete", Content: "English commit"})
	if removed.Error != "" {
		t.Fatalf("unique content remove error: %s", removed.Error)
	}
	if !removed.OK || !strings.Contains(removed.ChangedItem, "English commit") {
		t.Fatalf("unique content remove did not return deleted item: %+v", removed)
	}
}

func TestMemoryTool_SectionLineNumberFallsBackToGlobalWhenItMatchesSection(t *testing.T) {
	store := &fakeMemoryStore{data: map[int64]string{
		42: strings.Join([]string{
			"## User Background",
			"- background 1",
			"- background 2",
			"",
			"## User Preferences",
			"- preference 1",
			"- preference 2",
			"- stale preference",
		}, "\n"),
	}}
	tool := NewMemoryTool(store, 42, 1)
	out := runMemory(t, tool, MemoryInput{Action: "remove", Section: "user_preferences", LineNumber: 5})
	if out.Error != "" {
		t.Fatalf("expected global line fallback, got error: %s", out.Error)
	}
	if !out.OK || !strings.Contains(out.ChangedItem, "stale preference") {
		t.Fatalf("stale item should be removed: %+v", out)
	}
}

func TestMemoryTool_ReadEmpty(t *testing.T) {
	tool := NewMemoryTool(&fakeMemoryStore{data: map[int64]string{}}, 1, 1)
	r := runMemory(t, tool, MemoryInput{Action: "read"})
	if !r.OK || r.MemoryText != "" || r.Error != "" {
		t.Errorf("empty read should yield empty memory_text no error, got %+v", r)
	}
	if !strings.Contains(r.Message, "empty") {
		t.Errorf("expected empty message, got %q", r.Message)
	}
}

func TestMemoryTool_SessionIsolation(t *testing.T) {
	store := &fakeMemoryStore{data: map[int64]string{}}
	runMemory(t, NewMemoryTool(store, 1, 1), MemoryInput{Action: "write", Content: "s1"})
	runMemory(t, NewMemoryTool(store, 2, 1), MemoryInput{Action: "write", Content: "s2"})

	r1 := runMemory(t, NewMemoryTool(store, 1, 1), MemoryInput{Action: "read"})
	if !strings.Contains(r1.MemoryText, "s1") || strings.Contains(r1.MemoryText, "s2") {
		t.Errorf("session 1 leaked: %q", r1.MemoryText)
	}
}

func TestMemoryTool_RejectsOversize(t *testing.T) {
	tool := NewMemoryTool(&fakeMemoryStore{data: map[int64]string{}}, 1, 1)
	big := strings.Repeat("x", MemoryToolMaxChars+1)
	out := runMemory(t, tool, MemoryInput{Action: "write", Content: big})
	if out.Error == "" {
		t.Error("expected oversize rejection")
	}
}

func TestMemoryTool_UsesConfiguredMaxChars(t *testing.T) {
	tool := NewMemoryToolWithMaxChars(&fakeMemoryStore{data: map[int64]string{}}, 1, 1, MemoryToolMaxChars*2)
	big := strings.Repeat("x", MemoryToolMaxChars+1)
	out := runMemory(t, tool, MemoryInput{Action: "write", Content: big})
	if out.Error != "" {
		t.Fatalf("expected configured larger limit to pass, got %s", out.Error)
	}
}

func TestMemoryTool_UnknownAction(t *testing.T) {
	tool := NewMemoryTool(&fakeMemoryStore{data: map[int64]string{}}, 1, 1)
	out := runMemory(t, tool, MemoryInput{Action: "nuke"})
	if out.Error == "" {
		t.Error("expected error for unknown action")
	}
}

func TestMemoryTool_InvalidJSONReturnsStructuredError(t *testing.T) {
	tool := NewMemoryTool(&fakeMemoryStore{data: map[int64]string{}}, 1, 1)
	raw, err := tool.InvokableRun(context.Background(), `{`)
	if err != nil {
		t.Fatalf("InvokableRun returned Go error: %v", err)
	}
	var out MemoryOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.OK || out.Error == "" {
		t.Fatalf("expected structured failure, got %+v", out)
	}
}

func TestMemoryTool_InternalFailureReturnsGoError(t *testing.T) {
	tool := NewMemoryTool(&fakeMemoryStore{
		data:   map[int64]string{},
		getErr: errors.New("postgres://fixture:secret@db.example/effchat /srv/private/memory"),
	}, 1, 1)
	args, _ := json.Marshal(MemoryInput{Action: "read"})
	raw, err := tool.InvokableRun(context.Background(), string(args))
	if err == nil {
		t.Fatalf("expected internal failure, got result %q", raw)
	}
	if raw != "" || !strings.Contains(err.Error(), "read memory") || !strings.Contains(err.Error(), "fixture:secret") {
		t.Fatalf("internal failure was not preserved for Tool governance: raw=%q err=%v", raw, err)
	}
}

func TestMemoryToolInfo_GuidesExplicitRememberRequests(t *testing.T) {
	info, err := NewMemoryTool(&fakeMemoryStore{data: map[int64]string{}}, 1, 1).Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error: %v", err)
	}
	for _, want := range []string{`action="add"`, `action="replace"`, `action="remove"`, "specific section", "请更新这个决策", "以后如果我问你", "fictional", "project_context", "exact codes", "compact", "Do not quote the JSON", "cannot remember, update, or forget", "you are lying"} {
		if !strings.Contains(info.Desc, want) {
			t.Fatalf("memory tool description missing %q:\n%s", want, info.Desc)
		}
	}
}

func TestMemoryTool_AddRequiresSection(t *testing.T) {
	tool := NewMemoryTool(&fakeMemoryStore{data: map[int64]string{}}, 1, 1)
	out := runMemory(t, tool, MemoryInput{Action: "add", Content: "Use Chinese by default."})
	if out.Error == "" || !strings.Contains(out.Error, "section is required") {
		t.Fatalf("expected section required error, got %+v", out)
	}
}
