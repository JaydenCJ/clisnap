// Package config loads the optional project configuration from
// <snapshot-dir>/config.json.
//
// The config file lets a project set its default redactor list once and
// define custom redaction rules for tool-specific volatility (build IDs,
// request IDs, port numbers). Parsing is strict — unknown keys are
// rejected — because a typo in a testing tool's config that is silently
// ignored produces snapshots that assert the wrong thing.
//
// Example config.json:
//
//	{
//	  "redact": ["timestamp", "pid", "build-id"],
//	  "rules": [
//	    {
//	      "name": "build-id",
//	      "pattern": "build-[0-9a-f]{8}",
//	      "replace": "build-<ID>"
//	    }
//	  ]
//	}
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JaydenCJ/clisnap/internal/redact"
)

// FileName is the config file name inside the snapshot directory.
const FileName = "config.json"

// Config is the parsed project configuration. The zero value (no file
// present) means: built-in defaults, no custom rules.
type Config struct {
	// Redact, when non-empty, replaces the built-in default redactor list
	// for new recordings. Existing snapshots are unaffected: they carry
	// their own redactor list.
	Redact []string `json:"redact,omitempty"`
	// Rules defines project-specific redaction rules, applied before the
	// built-ins. A rule participates only when its name is selected (via
	// Redact, --redact, or the default union, see cli.resolveRedactors).
	Rules []redact.CustomRule `json:"rules,omitempty"`
}

// Load reads dir/config.json. A missing file yields an empty Config; a
// present-but-invalid file is always an error.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	cfg := &Config{}
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	// Reject a second JSON document; a config file is exactly one object.
	if dec.More() {
		return nil, fmt.Errorf("%s: trailing data after config object", path)
	}
	// Validate eagerly so 'record' and 'check' fail with a config error,
	// not a mid-run redaction error. Building an engine over the full
	// rule surface exercises every name and pattern.
	names := append(append([]string{}, cfg.Redact...), customNames(cfg.Rules)...)
	if _, err := redact.BuildWithHome(redact.Normalize(names), cfg.Rules, ""); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return cfg, nil
}

func customNames(rules []redact.CustomRule) []string {
	names := make([]string, 0, len(rules))
	for _, r := range rules {
		names = append(names, r.Name)
	}
	return names
}

// CustomNames returns the names of all custom rules in cfg, in order.
func (c *Config) CustomNames() []string {
	return customNames(c.Rules)
}
