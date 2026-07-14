// Tests for the unified diff engine. Failing snapshots are read through
// these diffs, so the output format is pinned exactly: wrong line numbers
// or missing no-newline markers would send users debugging the wrong line.
package diff

import (
	"strings"
	"testing"
)

func TestEqualInputsProduceEmptyDiff(t *testing.T) {
	for _, s := range []string{"same\ntext\n", "", "no newline"} {
		if d := Unified("a", "b", s, s, 3); d != "" {
			t.Fatalf("Unified(%q, %q) = %q, want empty", s, s, d)
		}
	}
}

func TestSingleLineChange(t *testing.T) {
	got := Unified("old", "new", "hello\n", "goodbye\n", 3)
	want := "--- old\n" +
		"+++ new\n" +
		"@@ -1 +1 @@\n" +
		"-hello\n" +
		"+goodbye\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestChangeInsideStableOutputGetsContext(t *testing.T) {
	a := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n"
	b := "l1\nl2\nl3\nl4\nCHANGED\nl6\nl7\nl8\nl9\n"
	got := Unified("a", "b", a, b, 3)
	want := "--- a\n" +
		"+++ b\n" +
		"@@ -2,7 +2,7 @@\n" +
		" l2\n l3\n l4\n" +
		"-l5\n" +
		"+CHANGED\n" +
		" l6\n l7\n l8\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEdgePlacementHunks(t *testing.T) {
	// Insertions/deletions at the very edges of the text exercise the
	// zero-length range headers ("N,0") that trip up naive formatters.
	cases := []struct{ desc, a, b, want string }{
		{"insertion at end", "x\n", "x\nnew line\n",
			"--- a\n+++ b\n@@ -1 +1,2 @@\n x\n+new line\n"},
		{"deletion at start", "gone\nkeep\n", "keep\n",
			"--- a\n+++ b\n@@ -1,2 +1 @@\n-gone\n keep\n"},
		{"empty to content", "", "line\n",
			"--- a\n+++ b\n@@ -0,0 +1 @@\n+line\n"},
		{"content to empty", "line\n", "",
			"--- a\n+++ b\n@@ -1 +0,0 @@\n-line\n"},
	}
	for _, c := range cases {
		if got := Unified("a", "b", c.a, c.b, 3); got != c.want {
			t.Errorf("%s: got:\n%s\nwant:\n%s", c.desc, got, c.want)
		}
	}
}

func TestDistantChangesGetSeparateHunks(t *testing.T) {
	// 20 equal lines between two changes: with context 3 the hunks must
	// not merge, and each must carry correct line numbers.
	var aL, bL []string
	for i := 1; i <= 24; i++ {
		aL = append(aL, "same")
		bL = append(bL, "same")
	}
	aL[1] = "first-old"
	bL[1] = "first-new"
	aL[22] = "second-old"
	bL[22] = "second-new"
	got := Unified("a", "b", strings.Join(aL, "\n")+"\n", strings.Join(bL, "\n")+"\n", 3)
	if strings.Count(got, "@@ -") != 2 {
		t.Fatalf("want 2 hunks, got:\n%s", got)
	}
	// Second change is on line 23 of 24, so trailing context is clipped
	// to the single remaining line: 5 lines total starting at 20.
	if !strings.Contains(got, "@@ -20,5 +20,5 @@") {
		t.Fatalf("second hunk mispositioned:\n%s", got)
	}
}

func TestNearbyChangesMergeIntoOneHunk(t *testing.T) {
	a := "1\n2\nX\n4\n5\nY\n7\n8\n"
	b := "1\n2\nx\n4\n5\ny\n7\n8\n"
	got := Unified("a", "b", a, b, 3)
	if strings.Count(got, "@@ -") != 1 {
		t.Fatalf("want 1 merged hunk, got:\n%s", got)
	}
}

func TestZeroContextShowsOnlyChanges(t *testing.T) {
	got := Unified("a", "b", "1\n2\n3\n", "1\nX\n3\n", 0)
	want := "--- a\n+++ b\n@@ -2 +2 @@\n-2\n+X\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNoTrailingNewlineMarkerOnBothSides(t *testing.T) {
	got := Unified("a", "b", "end", "END", 3)
	want := "--- a\n+++ b\n@@ -1 +1 @@\n" +
		"-end\n\\ No newline at end of file\n" +
		"+END\n\\ No newline at end of file\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTrailingNewlineOnlyChangeIsDetected(t *testing.T) {
	// Identical visible text, but one side stopped printing the final
	// newline — a real behavior change that naive line diffs miss.
	got := Unified("a", "b", "done\n", "done", 3)
	if got == "" {
		t.Fatal("newline-only change produced empty diff")
	}
	if !strings.Contains(got, "\\ No newline at end of file") {
		t.Fatalf("marker missing:\n%s", got)
	}
	if !strings.Contains(got, "-done\n") || !strings.Contains(got, "+done\n") {
		t.Fatalf("changed line not shown on both sides:\n%s", got)
	}
}

func TestMyersFindsMinimalScript(t *testing.T) {
	// The classic ABCABBA/CBABAC example: a minimal script has 5 edits.
	// Count edit lines to verify we are not degrading to delete-all/
	// insert-all (which would be 13).
	a := "A\nB\nC\nA\nB\nB\nA\n"
	b := "C\nB\nA\nB\nA\nC\n"
	got := Unified("a", "b", a, b, 0)
	edits := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			edits++
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			edits++
		}
	}
	if edits != 5 {
		t.Fatalf("edit count = %d, want minimal 5:\n%s", edits, got)
	}
}

func TestOversizedInputFallsBackToReplaceHunk(t *testing.T) {
	// Above the size cap the engine must stay correct (all differences
	// shown) even though the script is no longer minimal.
	old := myersCap
	myersCap = 4
	defer func() { myersCap = old }()

	a := "p\n1\n2\n3\n4\n5\nq\n"
	b := "p\n6\n7\n8\n9\n0\nq\n"
	got := Unified("a", "b", a, b, 1)
	for _, want := range []string{"-1", "-5", "+6", "+0", " p", " q"} {
		if !strings.Contains(got, want+"\n") {
			t.Fatalf("fallback diff missing %q:\n%s", want, got)
		}
	}
}

func TestCommonPrefixSuffixTrimmedBeforeMyers(t *testing.T) {
	// With the cap forced tiny, a change between long equal regions still
	// diffs minimally — proof that trimming happens before the cap check.
	old := myersCap
	myersCap = 4
	defer func() { myersCap = old }()

	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "same")
	}
	a := strings.Join(lines, "\n") + "\nold-tail\n" + strings.Join(lines, "\n") + "\n"
	b := strings.Join(lines, "\n") + "\nnew-tail\n" + strings.Join(lines, "\n") + "\n"
	got := Unified("a", "b", a, b, 3)
	if strings.Count(got, "@@ -") != 1 || !strings.Contains(got, "-old-tail\n+new-tail\n") {
		t.Fatalf("trim failed:\n%s", got)
	}
}

func TestHunkHeaderOmitsCountOfOne(t *testing.T) {
	// git omits ",1"; tools that parse unified diffs expect the same.
	got := Unified("a", "b", "x\n", "y\n", 3)
	if !strings.Contains(got, "@@ -1 +1 @@") {
		t.Fatalf("header style drift:\n%s", got)
	}
}

func TestLargeIdenticalPrefixFastPath(t *testing.T) {
	// 100k identical lines plus one change must complete instantly via
	// trimming — this guards against a quadratic regression.
	var sb strings.Builder
	for i := 0; i < 100000; i++ {
		sb.WriteString("stable line\n")
	}
	a := sb.String() + "old\n"
	b := sb.String() + "new\n"
	got := Unified("a", "b", a, b, 3)
	if !strings.Contains(got, "-old\n+new\n") {
		t.Fatalf("diff wrong:\n%.200s", got)
	}
}
