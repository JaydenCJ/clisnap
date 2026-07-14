// Package diff produces unified diffs between two texts.
//
// It implements Myers' O(ND) shortest-edit-script algorithm on lines,
// with common prefix/suffix trimming so the typical snapshot failure — a
// few changed lines in a long stable output — is found in near-linear
// time. Inputs past a size cap fall back to a plain replace hunk instead
// of risking pathological memory use; correctness is preserved, only
// minimality is sacrificed.
//
// Missing trailing newlines are significant (a CLI that stops printing
// "\n" has changed behavior) and are rendered git-style with a
// "\ No newline at end of file" marker.
package diff

import (
	"fmt"
	"strings"
)

// noNLTag marks the final line of a text that lacks a trailing newline.
// NUL cannot appear inside a split line's content boundary meaningfully
// here because it is appended, making "foo" (no newline) compare unequal
// to "foo\n" while remaining printable after the tag is stripped.
const noNLTag = "\x00"

// myersCap bounds len(a)+len(b) after trimming before the quadratic-worst-
// case Myers search is abandoned for a linear fallback. Variable, not
// const, so tests can lower it to exercise the fallback path.
var myersCap = 20000

type opKind int

const (
	opEq opKind = iota
	opDel
	opIns
)

// op is one edit-script step; text holds the (possibly tagged) line.
type op struct {
	kind opKind
	text string
}

// Unified returns a unified diff of a against b, or "" when they are
// byte-identical. aName/bName become the ---/+++ header labels; context
// is the number of surrounding equal lines per hunk (git uses 3).
func Unified(aName, bName, a, b string, context int) string {
	if a == b {
		return ""
	}
	if context < 0 {
		context = 0
	}
	aLines := tagLines(a)
	bLines := tagLines(b)
	ops := editScript(aLines, bLines)
	hunks := groupHunks(ops, context)

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", aName)
	fmt.Fprintf(&sb, "+++ %s\n", bName)
	for _, h := range hunks {
		sb.WriteString(h.header())
		for _, o := range h.ops {
			switch o.kind {
			case opEq:
				writeLine(&sb, ' ', o.text)
			case opDel:
				writeLine(&sb, '-', o.text)
			case opIns:
				writeLine(&sb, '+', o.text)
			}
		}
	}
	return sb.String()
}

// tagLines splits text into lines and tags the last one when the text
// lacks a trailing newline, so that difference is visible to the line
// comparison.
func tagLines(text string) []string {
	if text == "" {
		return nil
	}
	noNL := !strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if noNL {
		lines[len(lines)-1] += noNLTag
	}
	return lines
}

func writeLine(sb *strings.Builder, prefix byte, text string) {
	tagged := strings.HasSuffix(text, noNLTag)
	sb.WriteByte(prefix)
	sb.WriteString(strings.TrimSuffix(text, noNLTag))
	sb.WriteByte('\n')
	if tagged {
		sb.WriteString("\\ No newline at end of file\n")
	}
}

// editScript computes the line-level edit script from a to b.
func editScript(a, b []string) []op {
	// Trim the common prefix and suffix first: snapshot diffs are almost
	// always a small change inside a large equal region, and trimming
	// makes those cases O(n) regardless of the middle algorithm.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	aRest, bRest := a[pre:], b[pre:]
	suf := 0
	for suf < len(aRest) && suf < len(bRest) &&
		aRest[len(aRest)-1-suf] == bRest[len(bRest)-1-suf] {
		suf++
	}
	aMid := aRest[:len(aRest)-suf]
	bMid := bRest[:len(bRest)-suf]

	ops := make([]op, 0, pre+len(aMid)+len(bMid)+suf)
	for _, l := range a[:pre] {
		ops = append(ops, op{opEq, l})
	}
	if len(aMid)+len(bMid) > myersCap {
		ops = append(ops, replaceScript(aMid, bMid)...)
	} else {
		ops = append(ops, myers(aMid, bMid)...)
	}
	for _, l := range aRest[len(aRest)-suf:] {
		ops = append(ops, op{opEq, l})
	}
	return ops
}

// replaceScript is the oversized-input fallback: delete all of a, insert
// all of b. Not minimal, always correct.
func replaceScript(a, b []string) []op {
	ops := make([]op, 0, len(a)+len(b))
	for _, l := range a {
		ops = append(ops, op{opDel, l})
	}
	for _, l := range b {
		ops = append(ops, op{opIns, l})
	}
	return ops
}

// myers runs the classic forward O(ND) algorithm, keeping a snapshot of
// the furthest-reaching frontier per depth for backtracking.
func myers(a, b []string) []op {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	maxD := n + m
	offset := maxD
	v := make([]int, 2*maxD+2)
	var trace [][]int

	found := -1
search:
	for d := 0; d <= maxD; d++ {
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap) // state after depth d-1
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1] // step down: insertion from b
			} else {
				x = v[offset+k-1] + 1 // step right: deletion from a
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = d
				break search
			}
		}
	}

	// Backtrack from (n, m) to (0, 0), emitting ops in reverse.
	var rev []op
	x, y := n, m
	for d := found; d > 0; d-- {
		vprev := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vprev[offset+k-1] < vprev[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vprev[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY { // snake: equal run
			x--
			y--
			rev = append(rev, op{opEq, a[x]})
		}
		if prevK == k+1 {
			y--
			rev = append(rev, op{opIns, b[y]})
		} else {
			x--
			rev = append(rev, op{opDel, a[x]})
		}
		x, y = prevX, prevY
	}
	for x > 0 && y > 0 { // leading snake at depth 0
		x--
		y--
		rev = append(rev, op{opEq, a[x]})
	}
	// Reverse in place.
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// hunk is a contiguous run of ops rendered under one @@ header.
type hunk struct {
	aStart, aLen int // 1-based start and length on the a side
	bStart, bLen int
	ops          []op
}

func (h hunk) header() string {
	return fmt.Sprintf("@@ -%s +%s @@\n", rangeStr(h.aStart, h.aLen), rangeStr(h.bStart, h.bLen))
}

// rangeStr renders a unified-format range, omitting ",1" like git does
// and using "start-1,0" positioning for empty ranges.
func rangeStr(start, length int) string {
	if length == 1 {
		return fmt.Sprintf("%d", start)
	}
	if length == 0 {
		return fmt.Sprintf("%d,0", start-1)
	}
	return fmt.Sprintf("%d,%d", start, length)
}

// groupHunks selects the ops to display: every non-equal op plus up to
// `context` equal lines on each side, merging hunks whose context regions
// touch or overlap.
func groupHunks(ops []op, context int) []hunk {
	// keep[i] marks ops that appear in some hunk.
	keep := make([]bool, len(ops))
	for i, o := range ops {
		if o.kind == opEq {
			continue
		}
		lo := i - context
		if lo < 0 {
			lo = 0
		}
		hi := i + context
		if hi > len(ops)-1 {
			hi = len(ops) - 1
		}
		for j := lo; j <= hi; j++ {
			keep[j] = true
		}
	}

	// Walk ops tracking line numbers on both sides, slicing kept runs
	// into hunks.
	var hunks []hunk
	aLine, bLine := 0, 0 // lines consumed so far on each side
	i := 0
	for i < len(ops) {
		if !keep[i] {
			switch ops[i].kind {
			case opEq:
				aLine++
				bLine++
			case opDel:
				aLine++
			case opIns:
				bLine++
			}
			i++
			continue
		}
		h := hunk{aStart: aLine + 1, bStart: bLine + 1}
		for i < len(ops) && keep[i] {
			o := ops[i]
			h.ops = append(h.ops, o)
			switch o.kind {
			case opEq:
				aLine++
				bLine++
				h.aLen++
				h.bLen++
			case opDel:
				aLine++
				h.aLen++
			case opIns:
				bLine++
				h.bLen++
			}
			i++
		}
		hunks = append(hunks, h)
	}
	return hunks
}
