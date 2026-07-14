// Tests for config loading. The config file belongs to a *testing* tool,
// so silent acceptance of a broken config would corrupt what users think
// they are asserting — strictness is the feature under test here.
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadMissingFileYieldsEmptyConfig(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Redact) != 0 || len(cfg.Rules) != 0 {
		t.Fatalf("got %+v, want empty config", cfg)
	}
}

func TestLoadValidConfig(t *testing.T) {
	dir := writeConfig(t, `{
	  "redact": ["timestamp", "build-id"],
	  "rules": [
	    {"name": "build-id", "pattern": "build-[0-9a-f]{8}", "replace": "build-<ID>"}
	  ]
	}`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Redact) != 2 || len(cfg.Rules) != 1 || cfg.Rules[0].Name != "build-id" {
		t.Fatalf("got %+v", cfg)
	}
	if names := cfg.CustomNames(); len(names) != 1 || names[0] != "build-id" {
		t.Fatalf("CustomNames = %v", names)
	}
}

func TestLoadRejectsInvalidConfigs(t *testing.T) {
	cases := []struct{ desc, content, wantErr string }{
		// "redactors" instead of "redact" silently ignored would mean
		// the user's chosen rules never run.
		{"unknown key", `{"redactors": ["pid"]}`, "redactors"},
		{"invalid JSON", `{"redact": [`, "config.json"},
		{"second JSON document", `{} {"second": true}`, "trailing"},
		{"unknown redactor typo", `{"redact": ["timestmap"]}`, "timestmap"},
		{"uncompilable rule pattern",
			`{"rules": [{"name": "x", "pattern": "([", "replace": "y"}]}`, "x"},
		{"invalid rule name",
			`{"rules": [{"name": "Bad Name", "pattern": "a", "replace": "b"}]}`, "Bad Name"},
	}
	for _, c := range cases {
		dir := writeConfig(t, c.content)
		_, err := Load(dir)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want mention of %q", c.desc, err, c.wantErr)
		}
		// Every rejection must name the config file so the user knows
		// where to look.
		if err != nil && !strings.Contains(err.Error(), FileName) {
			t.Errorf("%s: err = %v, does not name %s", c.desc, err, FileName)
		}
	}
}
