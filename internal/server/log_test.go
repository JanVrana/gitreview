package server

import (
	"strings"
	"testing"

	"github.com/JanVrana/gitreview/internal/git"
	"github.com/JanVrana/gitreview/internal/testrepo"
)

// The branch log lists the commits since the merge-base; commit= shows one
// of them against its parent, without the uncommitted changes.
func TestLogAndCommit(t *testing.T) {
	ts, dir := newTest(t)
	testrepo.Write(t, dir, "second.txt", "two\n")
	testrepo.Git(t, dir, "add", "second.txt")
	testrepo.Git(t, dir, "commit", "-q", "-m", "second commit")

	var lg struct {
		Commits []git.Commit
		More    bool
	}
	if code := get(t, ts, "/api/log?base=develop", &lg); code != 200 {
		t.Fatalf("log: %d", code)
	}
	if len(lg.Commits) != 2 || lg.Commits[0].Subject != "second commit" || lg.Commits[1].Subject != "feature work" || lg.More {
		t.Fatalf("log: %+v", lg)
	}
	first := lg.Commits[1]

	var res struct {
		Compare compare
		Files   []git.FileChange
	}
	if code := get(t, ts, "/api/files?base=develop&commit="+first.Sha, &res); code != 200 {
		t.Fatalf("files: %d", code)
	}
	var paths []string
	for _, f := range res.Files {
		paths = append(paths, f.Status+" "+f.Path)
		if f.Uncommitted {
			t.Errorf("%s marked uncommitted", f.Path)
		}
	}
	if got := strings.Join(paths, ","); got != "D delete-me.txt,M main.go,R renamed.txt" {
		t.Fatalf("files: %s", got)
	}
	if res.Compare.Commit == nil || res.Compare.Commit.Sha != first.Sha || res.Compare.Left.Rev != first.Parents[0] || res.Compare.Right.Rev != first.Sha {
		t.Fatalf("compare: %+v", res.Compare)
	}
	var d diffResp
	if code := get(t, ts, "/api/diff?base=develop&commit="+first.Sha+"&path=main.go", &d); code != 200 || d.Right.Hash != "" || len(d.Right.Lines) != 32 {
		t.Fatalf("diff %d: %d lines, hash %q", code, len(d.Right.Lines), d.Right.Hash)
	}

	// the root commit is compared with the empty tree
	root := strings.TrimSpace(testrepo.Git(t, dir, "rev-list", "--max-parents=0", "HEAD"))
	if code := get(t, ts, "/api/files?base=develop&commit="+root, &res); code != 200 || len(res.Files) != 4 || res.Compare.Left.Rev != git.EmptyTree {
		t.Fatalf("root: %d %+v", code, res.Files)
	}
	if code := get(t, ts, "/api/diff?base=develop&commit="+root+"&path=main.go", &d); code != 200 || len(d.Right.Lines) != 30 {
		t.Fatalf("root diff: %d", code)
	}

	for bad, want := range map[string]int{"xyz": 400, strings.Repeat("a", 40): 404, first.Sha[:8]: 400} {
		if code := get(t, ts, "/api/files?base=develop&commit="+bad, nil); code != want {
			t.Errorf("commit=%s: %d, want %d", bad, code, want)
		}
	}
}
