package repository

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestFontSlotTransactionsPreserveIndependentUpdates(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	repoA := NewFontRepository(db)
	repoB := NewFontRepository(db)
	baseline := createFontFixture(t, repoA, "Baseline")
	chinese := createFontFixture(t, repoA, "Chinese")
	latin := createFontFixture(t, repoA, "Latin")

	for _, slot := range []ChatFontSlot{ChatFontSlotChinese, ChatFontSlotLatin, ChatFontSlotCode} {
		if _, err := repoA.SetSelectedSlot(slot, &baseline.ID); err != nil {
			t.Fatalf("set baseline %s: %v", slot, err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, err := repoA.SetSelectedSlot(ChatFontSlotChinese, &chinese.ID)
		errs <- err
	}()
	go func() {
		defer wait.Done()
		<-start
		_, err := repoB.SetSelectedSlot(ChatFontSlotLatin, &latin.ID)
		errs <- err
	}()
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent slot update: %v", err)
		}
	}

	selection, err := repoA.GetSelectedIDs()
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedFontID(t, "chinese", selection.Chinese, chinese.ID)
	assertSelectedFontID(t, "latin", selection.Latin, latin.ID)
	assertSelectedFontID(t, "code", selection.Code, baseline.ID)
	legacy, err := repoA.GetSelectedID()
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedFontID(t, "legacy", legacy, chinese.ID)
}

func TestChineseSelectionRollsBackWithLegacyMirror(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	repo := NewFontRepository(db)
	baseline := createFontFixture(t, repo, "Baseline")
	replacement := createFontFixture(t, repo, "Replacement")
	if _, err := repo.SetSelectedSlot(ChatFontSlotChinese, &baseline.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION fail_font_legacy_mirror() RETURNS trigger AS $$
		BEGIN
			IF NEW.key = '%s' AND NEW.value = to_jsonb(%d::bigint) THEN
				RAISE EXCEPTION 'fixture legacy mirror failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_font_legacy_mirror_trigger
		BEFORE INSERT OR UPDATE ON system_config
		FOR EACH ROW EXECUTE FUNCTION fail_font_legacy_mirror();
	`, selectedChatFontConfigKey, replacement.ID)); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}

	if _, err := repo.SetSelectedSlot(ChatFontSlotChinese, &replacement.ID); err == nil {
		t.Fatal("selection succeeded despite legacy mirror failure")
	}
	selection, err := repo.GetSelectedIDs()
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedFontID(t, "chinese", selection.Chinese, baseline.ID)
	legacy, err := repo.GetSelectedID()
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedFontID(t, "legacy", legacy, baseline.ID)
}

func TestFontSelectionDistinguishesMissingSlotsFromExplicitDefaults(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	repo := NewFontRepository(db)
	legacy := createFontFixture(t, repo, "Legacy")
	if _, err := repo.SetSelectedSlot(ChatFontSlotChinese, &legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetSelectedSlot(ChatFontSlotLatin, &legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE system_config SET value = 'null'::jsonb WHERE key = $1`, selectedChatLatinFontConfigKey); err != nil {
		t.Fatal(err)
	}

	selection, err := repo.GetSelectedIDs()
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedFontID(t, "chinese", selection.Chinese, legacy.ID)
	if selection.Latin != nil {
		t.Fatalf("explicit latin default was restored from legacy: %v", *selection.Latin)
	}
	// The code slot key was never written, so legacy compatibility still applies.
	assertSelectedFontID(t, "missing code", selection.Code, legacy.ID)

	if _, err := db.Exec(`DELETE FROM system_config WHERE key IN ($1, $2, $3)`, selectedChatChineseFontConfigKey, selectedChatLatinFontConfigKey, selectedChatCodeFontConfigKey); err != nil {
		t.Fatal(err)
	}
	selection, err = repo.GetSelectedIDs()
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedFontID(t, "legacy-only chinese", selection.Chinese, legacy.ID)
	assertSelectedFontID(t, "legacy-only latin", selection.Latin, legacy.ID)
	assertSelectedFontID(t, "legacy-only code", selection.Code, legacy.ID)
}

func TestDisableAndDeleteClearOnlyOwnedSlotsAtomically(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	repo := NewFontRepository(db)
	disabled := createFontFixture(t, repo, "Disabled")
	kept := createFontFixture(t, repo, "Kept")
	deleted := createFontFixture(t, repo, "Deleted")

	if _, err := repo.SetSelectedSlot(ChatFontSlotChinese, &disabled.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetSelectedSlot(ChatFontSlotLatin, &disabled.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetSelectedSlot(ChatFontSlotCode, &kept.ID); err != nil {
		t.Fatal(err)
	}
	disabled.Enabled = false
	selection, err := repo.Update(disabled)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Chinese != nil || selection.Latin != nil {
		t.Fatalf("disabled font remained selected: %#v", selection)
	}
	assertSelectedFontID(t, "code", selection.Code, kept.ID)
	if _, err := repo.SetSelectedSlot(ChatFontSlotLatin, &disabled.ID); !errors.Is(err, ErrFontUnavailable) {
		t.Fatalf("select disabled font error = %v, want ErrFontUnavailable", err)
	}

	if _, err := repo.SetSelectedSlot(ChatFontSlotCode, &deleted.ID); err != nil {
		t.Fatal(err)
	}
	selection, err = repo.Delete(deleted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Code != nil {
		t.Fatalf("deleted font remained selected: %#v", selection)
	}
	if _, err := repo.Get(deleted.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted font lookup error = %v, want ErrNotFound", err)
	}
}

func TestDisableRollsBackWhenSelectionCleanupFails(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	repo := NewFontRepository(db)
	font := createFontFixture(t, repo, "Rollback")
	if _, err := repo.SetSelectedSlot(ChatFontSlotLatin, &font.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION fail_font_selection_clear() RETURNS trigger AS $$
		BEGIN
			IF NEW.key = '%s' AND NEW.value = 'null'::jsonb THEN
				RAISE EXCEPTION 'fixture selection cleanup failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_font_selection_clear_trigger
		BEFORE UPDATE ON system_config
		FOR EACH ROW EXECUTE FUNCTION fail_font_selection_clear();
	`, selectedChatLatinFontConfigKey)); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}

	font.Enabled = false
	if _, err := repo.Update(font); err == nil || !strings.Contains(err.Error(), "clear selected font") {
		t.Fatalf("disable error = %v, want cleanup failure", err)
	}
	stored, err := repo.Get(font.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled {
		t.Fatal("font disable committed despite selection cleanup failure")
	}
	selection, err := repo.GetSelectedIDs()
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedFontID(t, "latin", selection.Latin, font.ID)
}

func createFontFixture(t *testing.T, repo *FontRepository, name string) *model.FontAsset {
	t.Helper()
	font := &model.FontAsset{
		DisplayName: name,
		FamilyName:  name,
		FileName:    strings.ToLower(name) + ".woff2",
		FilePath:    filepath.Join(t.TempDir(), strings.ToLower(name)+".woff2"),
		MimeType:    "font/woff2",
		FileSize:    128,
		Checksum:    strings.Repeat(strings.ToLower(name[:1]), 64),
		Weight:      400,
		Style:       "normal",
		Enabled:     true,
	}
	if err := repo.Create(font); err != nil {
		t.Fatalf("create font %s: %v", name, err)
	}
	return font
}

func assertSelectedFontID(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s selection = %v, want %d", name, got, want)
	}
}
