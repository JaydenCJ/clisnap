// Tests for the snapshot store: file layout, overwrite protection, name
// validation (which doubles as the path-traversal defense), and listing.
package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) Store {
	t.Helper()
	return Store{Dir: filepath.Join(t.TempDir(), ".clisnap")}
}

func TestSaveCreatesDirThenLoadRoundTrips(t *testing.T) {
	st := testStore(t) // directory does not exist yet
	want := sample()
	if err := st.Save("greet", want, false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(st.Path("greet")); err != nil {
		t.Fatalf("snapshot file missing: %v", err)
	}
	got, err := st.Load("greet")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Stdout != want.Stdout || got.Exit != want.Exit {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSaveOverwriteNeedsForce(t *testing.T) {
	// Accidental re-record is how golden files silently rot; replacing a
	// snapshot must be an explicit act.
	st := testStore(t)
	if err := st.Save("greet", sample(), false); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	err := st.Save("greet", sample(), false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want overwrite refusal", err)
	}
	s2 := sample()
	s2.Stdout = "changed\n"
	if err := st.Save("greet", s2, true); err != nil {
		t.Fatalf("forced Save: %v", err)
	}
	got, _ := st.Load("greet")
	if got.Stdout != "changed\n" {
		t.Fatalf("got %q", got.Stdout)
	}
}

func TestSaveLeavesNoTempFileBehind(t *testing.T) {
	// The write-then-rename dance must clean up: stray temp files would
	// pollute 'git status' in every user's repo.
	st := testStore(t)
	if err := st.Save("greet", sample(), false); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, _ := os.ReadDir(st.Dir)
	if len(entries) != 1 || entries[0].Name() != "greet.snap" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("dir contains %v, want exactly [greet.snap]", names)
	}
}

func TestLoadMissingSnapshotErrors(t *testing.T) {
	st := testStore(t)
	_, err := st.Load("ghost")
	if err == nil || !strings.Contains(err.Error(), `"ghost"`) {
		t.Fatalf("err = %v, want named not-found error", err)
	}
}

func TestLoadCorruptSnapshotNamesFile(t *testing.T) {
	st := testStore(t)
	os.MkdirAll(st.Dir, 0o755)
	os.WriteFile(st.Path("bad"), []byte("garbage\n"), 0o644)
	_, err := st.Load("bad")
	if err == nil || !strings.Contains(err.Error(), "bad.snap") {
		t.Fatalf("err = %v, want error naming the file", err)
	}
}

func TestDeleteRemovesSnapshotAndRejectsGhosts(t *testing.T) {
	st := testStore(t)
	st.Save("greet", sample(), false)
	if err := st.Delete("greet"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if st.Exists("greet") {
		t.Fatal("snapshot still exists after Delete")
	}
	// A typo in 'clisnap rm' must be noticed, not silently succeed.
	if err := st.Delete("ghost"); err == nil {
		t.Fatal("deleting a missing snapshot succeeded")
	}
}

func TestListSortedAndIgnoresForeignFiles(t *testing.T) {
	st := testStore(t)
	st.Save("zeta", sample(), false)
	st.Save("alpha", sample(), false)
	// config.json and editor droppings live alongside snapshots.
	os.WriteFile(filepath.Join(st.Dir, "config.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(st.Dir, "notes.txt"), []byte("x"), 0o644)
	names, err := st.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "zeta" {
		t.Fatalf("got %v, want [alpha zeta]", names)
	}
}

func TestListMissingDirIsEmptyNotError(t *testing.T) {
	st := testStore(t) // dir never created
	names, err := st.List()
	if err != nil || names != nil {
		t.Fatalf("got %v, %v; want nil, nil", names, err)
	}
}

func TestValidNameRules(t *testing.T) {
	valid := []string{"greet", "api.v2", "a", "0start", "with-dash_and.dot"}
	for _, n := range valid {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false, want true", n)
		}
	}
	// Rejections include everything that could escape the store dir.
	invalid := []string{"", ".hidden", "-lead", "a/b", "../up", "a b", "a\nb", "café"}
	for _, n := range invalid {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true, want false", n)
		}
	}
}

func TestPathStaysInsideStoreDir(t *testing.T) {
	st := testStore(t)
	// Even for names that fail validation, Save/Load/Delete must refuse
	// before touching the filesystem.
	for _, n := range []string{"../escape", "a/b"} {
		if err := st.Save(n, sample(), false); err == nil {
			t.Errorf("Save(%q) succeeded, want validation error", n)
		}
		if _, err := st.Load(n); err == nil {
			t.Errorf("Load(%q) succeeded, want validation error", n)
		}
		if err := st.Delete(n); err == nil {
			t.Errorf("Delete(%q) succeeded, want validation error", n)
		}
	}
}
