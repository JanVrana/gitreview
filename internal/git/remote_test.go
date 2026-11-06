package git

import (
	"path/filepath"
	"testing"

	"github.com/JanVrana/gitreview/internal/testrepo"
)

func commit(t *testing.T, dir, file, content string) {
	t.Helper()
	testrepo.Write(t, dir, file, content)
	testrepo.Git(t, dir, "add", "-A")
	testrepo.Git(t, dir, "commit", "-q", "-m", file+": "+content)
}

func TestFetchAndFastForward(t *testing.T) {
	origin := t.TempDir()
	testrepo.Git(t, origin, "init", "-q", "-b", "develop")
	commit(t, origin, "a.txt", "1")
	testrepo.Git(t, origin, "branch", "master")
	testrepo.Git(t, origin, "branch", "main")

	root := t.TempDir()
	clone := filepath.Join(root, "clone")
	testrepo.Git(t, root, "clone", "-q", origin, clone)
	testrepo.Git(t, clone, "branch", "master", "origin/master")
	testrepo.Git(t, clone, "branch", "main", "origin/main")
	testrepo.Git(t, clone, "checkout", "-q", "-b", "topic")
	testrepo.Git(t, clone, "worktree", "add", "-q", filepath.Join(root, "wt"), "main")
	// local commit on master in the clone -> diverges from origin below
	testrepo.Git(t, clone, "checkout", "-q", "master")
	commit(t, clone, "local.txt", "x")
	testrepo.Git(t, clone, "checkout", "-q", "topic")

	// origin moves on
	commit(t, origin, "a.txt", "2")
	commit(t, origin, "a.txt", "3")
	for _, b := range []string{"master", "main"} {
		testrepo.Git(t, origin, "checkout", "-q", b)
		commit(t, origin, b+".txt", "new")
	}
	testrepo.Git(t, origin, "checkout", "-q", "develop")

	r, err := Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	if a, b, _ := r.AheadBehind("develop", "origin/develop"); a != 0 || b != 0 {
		t.Fatalf("before fetch: ahead %d behind %d, want 0 0", a, b)
	}
	if err := r.Fetch(); err != nil {
		t.Fatal(err)
	}
	if a, b, _ := r.AheadBehind("develop", "origin/develop"); a != 0 || b != 2 {
		t.Fatalf("after fetch: ahead %d behind %d, want 0 2", a, b)
	}

	want := map[string]string{"develop": Updated, "master": Diverged, "main": CheckedOut, "topic": NoUpstream}
	for branch, status := range want {
		u, err := r.FastForward(branch)
		if err != nil {
			t.Fatalf("%s: %v", branch, err)
		}
		if u.Status != status {
			t.Errorf("%s: status %s, want %s (%+v)", branch, u.Status, status, u)
		}
	}
	dev, _ := r.Verify("develop")
	up, _ := r.Verify("origin/develop")
	if dev != up {
		t.Error("develop was not fast-forwarded to origin/develop")
	}
	if u, _ := r.FastForward("develop"); u.Status != UpToDate {
		t.Errorf("second run: %s, want %s", u.Status, UpToDate)
	}
	if testrepo.Git(t, clone, "status", "--porcelain") != "" {
		t.Error("working tree changed")
	}
}
