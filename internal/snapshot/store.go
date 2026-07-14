package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Ext is the snapshot file extension.
const Ext = ".snap"

// nameRe validates snapshot names. Names become file names, so they must
// start alphanumeric (no dotfiles) and may not contain path separators —
// which also rules out traversal like "../x" without any special-casing.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidName reports whether name is a legal snapshot name.
func ValidName(name string) bool {
	return nameRe.MatchString(name) && !strings.Contains(name, "..")
}

// Store manages the snapshot directory (by default ".clisnap" in the
// project root, committed to version control alongside the code it tests).
type Store struct {
	Dir string
}

// Path returns the file path a snapshot name maps to.
func (st Store) Path(name string) string {
	return filepath.Join(st.Dir, name+Ext)
}

// Exists reports whether a snapshot with this name is on disk.
func (st Store) Exists(name string) bool {
	_, err := os.Stat(st.Path(name))
	return err == nil
}

// Save encodes and writes a snapshot. Unless overwrite is set, saving over
// an existing snapshot is refused — accidental re-record is how golden
// files silently rot, so replacing one must be an explicit choice
// (record --force, or check --update).
func (st Store) Save(name string, s *Snapshot, overwrite bool) error {
	if !ValidName(name) {
		return fmt.Errorf("invalid snapshot name %q (want [A-Za-z0-9][A-Za-z0-9._-]*)", name)
	}
	if !overwrite && st.Exists(name) {
		return fmt.Errorf("snapshot %q already exists (use --force to overwrite, or 'clisnap check --update')", name)
	}
	data, err := Encode(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(st.Dir, 0o755); err != nil {
		return fmt.Errorf("create snapshot dir: %v", err)
	}
	// Write-then-rename so a crash mid-write can never leave a truncated
	// snapshot that later fails to decode.
	tmp, err := os.CreateTemp(st.Dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("write snapshot: %v", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write snapshot: %v", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write snapshot: %v", err)
	}
	if err := os.Rename(tmpName, st.Path(name)); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write snapshot: %v", err)
	}
	return nil
}

// Load reads and decodes one snapshot.
func (st Store) Load(name string) (*Snapshot, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid snapshot name %q", name)
	}
	data, err := os.ReadFile(st.Path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no snapshot named %q in %s", name, st.Dir)
		}
		return nil, err
	}
	s, err := Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", st.Path(name), err)
	}
	return s, nil
}

// Raw returns the snapshot file bytes unparsed (for 'clisnap show').
func (st Store) Raw(name string) ([]byte, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid snapshot name %q", name)
	}
	data, err := os.ReadFile(st.Path(name))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no snapshot named %q in %s", name, st.Dir)
	}
	return data, err
}

// Delete removes one snapshot; a missing snapshot is an error so typos in
// 'clisnap rm' are noticed.
func (st Store) Delete(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	err := os.Remove(st.Path(name))
	if os.IsNotExist(err) {
		return fmt.Errorf("no snapshot named %q in %s", name, st.Dir)
	}
	return err
}

// List returns all snapshot names, sorted. Non-.snap files (config.json,
// editor droppings) are ignored. A missing directory is an empty store,
// not an error, so 'clisnap list' works in a fresh project.
func (st Store) List() ([]string, error) {
	entries, err := os.ReadDir(st.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Ext) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), Ext)
		if ValidName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}
