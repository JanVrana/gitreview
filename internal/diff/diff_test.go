package diff

import (
	"reflect"
	"testing"
)

func TestParseHunks(t *testing.T) {
	out := []byte("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -3 +3 @@ func\n-a\n+b\n@@ -10,0 +11,2 @@\n+c\n+d\n@@ -20,3 +21,0 @@\n-e\n")
	got := ParseHunks(out)
	want := []Hunk{{3, 1, 3, 1}, {10, 0, 11, 2}, {20, 3, 21, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestBlocksCoverBothFiles(t *testing.T) {
	// left 25 lines, right 24 lines: modify line 3, insert 2 after line 10, delete 20-22
	hunks := []Hunk{{3, 1, 3, 1}, {10, 0, 11, 2}, {20, 3, 21, 0}}
	got := Blocks(hunks, 25, 24)
	want := []Block{
		{"equal", 0, 2, 0, 2},
		{"change", 2, 1, 2, 1},
		{"equal", 3, 7, 3, 7},
		{"change", 10, 0, 10, 2},
		{"equal", 10, 9, 12, 9},
		{"change", 19, 3, 21, 0},
		{"equal", 22, 3, 21, 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%v\nwant\n%v", got, want)
	}
	assertCoverage(t, got, 25, 24)
}

func TestBlocksInsertAtStartAndEnd(t *testing.T) {
	// insert 1 line at the beginning, append 2 lines at the end of a 3-line file
	got := Blocks([]Hunk{{0, 0, 1, 1}, {3, 0, 5, 2}}, 3, 6)
	assertCoverage(t, got, 3, 6)
	if got[0] != (Block{"change", 0, 0, 0, 1}) || got[len(got)-1] != (Block{"change", 3, 0, 4, 2}) {
		t.Fatalf("unexpected %v", got)
	}
}

func TestBlocksInconsistentFallsBack(t *testing.T) {
	got := Blocks([]Hunk{{50, 1, 50, 1}}, 10, 12)
	assertCoverage(t, got, 10, 12)
}

func assertCoverage(t *testing.T, bs []Block, lc, rc int) {
	t.Helper()
	l, r := 0, 0
	for _, b := range bs {
		if b.LeftStart != l || b.RightStart != r {
			t.Fatalf("gap at %v (expected %d,%d) in %v", b, l, r, bs)
		}
		if b.Type == "equal" && b.LeftLen != b.RightLen {
			t.Fatalf("unequal equal block %v", b)
		}
		l += b.LeftLen
		r += b.RightLen
	}
	if l != lc || r != rc {
		t.Fatalf("covers %d/%d, want %d/%d", l, r, lc, rc)
	}
}

func TestBlocksNeverNil(t *testing.T) {
	if b := Blocks(nil, 0, 0); b == nil {
		t.Fatal("nil blocks for empty files")
	}
	if b := Whole(0, 0); b == nil || len(b) != 0 {
		t.Fatalf("Whole(0,0) = %v", b)
	}
	if b := Whole(0, 3); len(b) != 1 || b[0].RightLen != 3 {
		t.Fatalf("Whole(0,3) = %v", b)
	}
}

func TestBlocksInconsistentNeverEqual(t *testing.T) {
	// hunk beyond the content: remaining lines must be a change, not "equal"
	got := Blocks([]Hunk{{2, 1, 2, 1}, {50, 1, 50, 1}}, 10, 10)
	assertCoverage(t, got, 10, 10)
	if last := got[len(got)-1]; last.Type != "change" {
		t.Fatalf("tail marked as %v", last)
	}
}
