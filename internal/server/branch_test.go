package server

import (
	"testing"

	"github.com/JanVrana/gitreview/internal/git"
	"github.com/JanVrana/gitreview/internal/testrepo"
)

// Any branch can be the right side without checking it out.
func TestCompareOtherBranch(t *testing.T) {
	ts, dir := newTest(t)
	testrepo.Git(t, dir, "stash", "-u", "-q")
	testrepo.Git(t, dir, "checkout", "-q", "-b", "other", "develop")
	testrepo.Write(t, dir, "other.txt", "only on other\n")
	testrepo.Git(t, dir, "add", "other.txt")
	testrepo.Git(t, dir, "commit", "-q", "-m", "other work")
	testrepo.Git(t, dir, "checkout", "-q", "feature")
	testrepo.Git(t, dir, "stash", "pop", "-q")

	var res struct {
		Compare compare
		Files   []git.FileChange
	}
	if code := get(t, ts, "/api/files?base=develop&right=branch:other", &res); code != 200 {
		t.Fatalf("files: %d", code)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "other.txt" || res.Files[0].Uncommitted {
		t.Fatalf("files: %+v", res.Files)
	}
	if !res.Compare.RightBranch || res.Compare.Right.Rev == "" || res.Compare.Right.Label[:6] != "other " {
		t.Fatalf("compare: %+v", res.Compare)
	}
	var d diffResp
	get(t, ts, "/api/diff?base=develop&right=branch:other&path=other.txt", &d)
	if len(d.Right.Lines) != 1 || d.Right.Hash != "" {
		t.Fatalf("diff: %+v", d)
	}
	if h := testrepo.Git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); h != "feature\n" {
		t.Fatalf("checked out %q", h)
	}
	for _, bad := range []string{"branch:nope", "branch:-x", "branch:"} {
		if code := get(t, ts, "/api/files?base=develop&right="+bad, nil); code != 400 {
			t.Errorf("%s: %d, want 400", bad, code)
		}
	}
}
