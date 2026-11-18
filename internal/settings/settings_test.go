package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsSurviveARestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "nested", "settings.json")
	want := Settings{
		Appearance: AppearanceDark, FontSize: 16, TabSize: 2, WordWrap: true,
		MarkdownView: MarkdownSource, DiffLayout: DiffSplit,
	}
	if err := Save(file, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
}

func TestLoadKeepsWhatItCanUse(t *testing.T) {
	dir := t.TempDir()
	if got, err := Load(filepath.Join(dir, "missing.json")); err != nil || got != Default() {
		t.Errorf("a missing file gave %+v, %v", got, err)
	}

	file := filepath.Join(dir, "settings.json")
	text := `{"appearance":"neon","fontSize":99,"tabSize":3,"wordWrap":true,"diffLayout":"split"}`
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.WordWrap = true
	want.DiffLayout = DiffSplit
	if got, err := Load(file); err != nil || got != want {
		t.Errorf("loaded %+v, %v; want %+v", got, err, want)
	}

	if err := os.WriteFile(file, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(file); err == nil || got != Default() {
		t.Errorf("a broken file gave %+v, %v", got, err)
	}
}
