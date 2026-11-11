package server

import (
	"strings"
	"testing"

	"github.com/JanVrana/gitreview/internal/git"
)

func TestBlame(t *testing.T) {
	ts, _ := newTest(t)
	type side = git.Blame
	var b struct{ Left, Right *side }
	const q = "/api/blame?base=develop&left=mergebase&right=worktree&ws=0&path="
	if code := get(t, ts, q+"main.go", &b); code != 200 || b.Left == nil || b.Right == nil {
		t.Fatalf("blame: %d %+v", code, b)
	}
	if len(b.Left.Lines) != 30 || len(b.Left.Commits) != 1 || b.Left.Commits[0].Summary != "base" {
		t.Errorf("left: %+v", b.Left)
	}
	who := func(i int) string { return b.Right.Commits[b.Right.Lines[i]].Summary }
	if who(0) != "base" || who(4) != "feature work" || b.Right.Commits[b.Right.Lines[4]].Author != "t" {
		t.Errorf("right: %q %q", who(0), who(4))
	}
	// untracked file: nothing committed, no left side
	var u struct{ Left, Right *side }
	if code := get(t, ts, q+"nov%C3%BD%20soubor.txt", &u); code != 200 || u.Left != nil || u.Right == nil ||
		len(u.Right.Lines) != 2 || strings.Trim(u.Right.Commits[0].Sha, "0") != "" {
		t.Fatalf("untracked: %d %+v", code, u.Right)
	}
	if code := get(t, ts, q+"bin.dat", nil); code != 400 {
		t.Errorf("binary: %d, want 400", code)
	}
}
