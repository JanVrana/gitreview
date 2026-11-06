package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JanVrana/gitreview/internal/diff"
	"github.com/JanVrana/gitreview/internal/testrepo"
)

type diffResp struct {
	Left, Right struct {
		Lines []string
		Hash  string
	}
	Blocks []diff.Block
}

func TestRevertBlock(t *testing.T) {
	ts, dir := newTest(t)
	const q = "?base=develop&left=mergebase&right=worktree&ws=0"
	var d diffResp
	get(t, ts, "/api/diff"+q+"&path=main.go", &d)
	if d.Right.Hash == "" {
		t.Fatal("no hash for a worktree file")
	}
	var mod *diff.Block
	for i, b := range d.Blocks {
		if b.Type == "change" && b.LeftLen == 1 && b.RightLen == 1 {
			mod = &d.Blocks[i]
			break
		}
	}
	if mod == nil {
		t.Fatalf("no modified line among %+v", d.Blocks)
	}
	req := func(hash string, b diff.Block) string {
		j, _ := json.Marshal(map[string]any{"path": "main.go", "hash": hash, "block": b})
		return string(j)
	}
	if code := post(t, ts, "/api/revert-block"+q, req(strings.Repeat("0", 64), *mod), nil); code != http.StatusConflict {
		t.Fatalf("stale hash: %d, want 409", code)
	}
	bogus := *mod
	bogus.LeftLen = 2
	if code := post(t, ts, "/api/revert-block"+q, req(d.Right.Hash, bogus), nil); code != http.StatusConflict {
		t.Fatalf("unknown block: %d, want 409", code)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if code := post(t, ts, "/api/revert-block"+q, req(d.Right.Hash, *mod), nil); code != 200 {
		t.Fatalf("revert: %d", code)
	}
	var d2 diffResp
	get(t, ts, "/api/diff"+q+"&path=main.go", &d2)
	if len(d2.Right.Lines) != len(d.Right.Lines) || d2.Right.Lines[mod.RightStart] != d.Left.Lines[mod.LeftStart] {
		t.Fatalf("line not reverted: %q", d2.Right.Lines[mod.RightStart])
	}
	if code := post(t, ts, "/api/revert-block/undo", "{}", nil); code != 200 {
		t.Fatalf("undo: %d", code)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "main.go")); string(after) != string(before) {
		t.Fatal("undo did not restore the file")
	}
	if code := post(t, ts, "/api/revert-block/undo", "{}", nil); code != http.StatusConflict {
		t.Fatalf("second undo: %d, want 409", code)
	}

	// committed-only view and added files offer no block revert
	var d3 diffResp
	get(t, ts, "/api/diff?base=develop&right=head&path=main.go", &d3)
	if d3.Right.Hash != "" {
		t.Error("hash offered for a committed side")
	}
	testrepo.Write(t, dir, "nový soubor.txt", "x\n")
	if code := post(t, ts, "/api/revert-block"+q, `{"path":"nový soubor.txt","block":{"leftLen":0,"rightLen":1}}`, nil); code != http.StatusBadRequest {
		t.Fatalf("untracked: %d, want 400", code)
	}
}
