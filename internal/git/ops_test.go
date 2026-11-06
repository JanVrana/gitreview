package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JanVrana/gitreview/internal/testrepo"
)

// gitEnv gives commits made through Repo (not testrepo.Git) an identity.
func gitEnv(t *testing.T) {
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
		"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_SYSTEM": "/dev/null",
	} {
		t.Setenv(k, v)
	}
}

func open(t *testing.T, dir string) *Repo {
	t.Helper()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCommitSelectedPaths(t *testing.T) {
	gitEnv(t)
	dir := testrepo.New(t) // uncommitted: main.go, bin.dat, untracked "nový soubor.txt"
	testrepo.Write(t, dir, "staged.txt", "staged\n")
	testrepo.Git(t, dir, "add", "staged.txt")
	r := open(t, dir)

	head := r.Head().Sha
	if _, err := r.Commit([]string{"main.go"}, "msg", "deadbeef"); !errors.Is(err, ErrHeadMoved) {
		t.Fatalf("stale head: %v, want ErrHeadMoved", err)
	}
	if _, err := r.Commit([]string{"renamed.txt"}, "msg", head); err == nil {
		t.Fatal("committing a clean file must fail")
	}
	sha, err := r.Commit([]string{"main.go", "nový soubor.txt"}, "review: accept", head)
	if err != nil {
		t.Fatal(err)
	}
	if sha == head {
		t.Fatal("HEAD did not move")
	}
	got := testrepo.Git(t, dir, "-c", "core.quotepath=false", "show", "--name-only", "--format=%s", "HEAD")
	if !strings.Contains(got, "review: accept") || !strings.Contains(got, "main.go") || !strings.Contains(got, "nový soubor.txt") ||
		strings.Contains(got, "staged.txt") || strings.Contains(got, "bin.dat") {
		t.Fatalf("unexpected commit content:\n%s", got)
	}
	pending, _ := r.Uncommitted()
	if _, ok := pending["staged.txt"]; !ok {
		t.Error("staged.txt should stay staged")
	}
	if _, ok := pending["bin.dat"]; !ok {
		t.Error("bin.dat should stay modified")
	}
	if _, ok := pending["main.go"]; ok {
		t.Error("main.go should be committed")
	}
}

func TestRevert(t *testing.T) {
	gitEnv(t)
	dir := testrepo.New(t)
	testrepo.Write(t, dir, "added.txt", "x\n")
	testrepo.Git(t, dir, "add", "added.txt")
	r := open(t, dir)
	for _, p := range []string{"main.go", "nový soubor.txt", "added.txt"} {
		if err := r.Revert(p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if err := r.Revert("renamed.txt"); err == nil {
		t.Error("reverting a clean file must fail")
	}
	if err := r.Revert("../outside"); err == nil {
		t.Error("path outside the repository accepted")
	}
	for _, p := range []string{"nový soubor.txt", "added.txt"} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if st := testrepo.Git(t, dir, "status", "--porcelain"); strings.TrimSpace(st) != "M bin.dat" {
		t.Fatalf("status after revert: %q", st)
	}
}

func TestPushAndPull(t *testing.T) {
	gitEnv(t)
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	testrepo.Git(t, root, "init", "-q", "--bare", "-b", "develop", origin)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	testrepo.Git(t, root, "clone", "-q", origin, a)
	commit(t, a, "x.txt", "1")
	testrepo.Git(t, a, "push", "-q", "origin", "develop")
	testrepo.Git(t, root, "clone", "-q", origin, b)

	ra := open(t, a)
	testrepo.Git(t, a, "checkout", "-q", "-b", "topic")
	commit(t, a, "x.txt", "2")
	res, err := ra.Push()
	if err != nil || !res.Created || res.Upstream != "origin/topic" || res.Commits != 1 {
		t.Fatalf("first push: %+v %v", res, err)
	}
	commit(t, a, "x.txt", "3")
	if res, err = ra.Push(); err != nil || res.Created || res.Commits != 1 {
		t.Fatalf("second push: %+v %v", res, err)
	}

	rb := open(t, b)
	testrepo.Git(t, b, "fetch", "-q")
	testrepo.Git(t, b, "checkout", "-q", "topic")
	commit(t, a, "x.txt", "4")
	ra.Push()
	u, err := rb.Pull()
	if err != nil || u.Status != Updated || u.Commits != 1 {
		t.Fatalf("pull: %+v %v", u, err)
	}

	// diverged: pull --ff-only refuses, push is rejected (never forced)
	commit(t, a, "x.txt", "5")
	ra.Push()
	commit(t, b, "y.txt", "local")
	if _, err := rb.Pull(); err == nil {
		t.Error("pull of a diverged branch must fail")
	}
	if _, err := rb.Push(); err == nil {
		t.Error("push of a diverged branch must be rejected")
	}
}

func TestStash(t *testing.T) {
	gitEnv(t)
	dir := testrepo.New(t)
	r := open(t, dir)
	testrepo.Git(t, dir, "stash", "push", "-q", "-u", "-m", "work in progress") // main.go, bin.dat, nový soubor.txt
	testrepo.Write(t, dir, "renamed.txt", "other\n")
	testrepo.Git(t, dir, "stash", "push", "-q", "-m", "second")

	list, err := r.Stashes()
	if err != nil || len(list) != 2 {
		t.Fatalf("stashes: %+v %v", list, err)
	}
	wip := list[1]
	if wip.Ref != "stash@{1}" || !strings.Contains(wip.Message, "work in progress") || !wip.Untracked || list[0].Untracked {
		t.Fatalf("unexpected stash entries: %+v", list)
	}
	files, err := r.StashFiles(wip)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Status
		if f.Path == "nový soubor.txt" && f.Src == "" {
			t.Error("untracked stash file without Src")
		}
	}
	if got["main.go"] != "M" || got["bin.dat"] != "M" || got["nový soubor.txt"] != "A" {
		t.Fatalf("stash files: %v", got)
	}

	// apply refuses when a stashed file has uncommitted changes
	testrepo.Write(t, dir, "main.go", "dirty\n")
	if err := r.StashApply(wip.Sha, true); err == nil || !strings.Contains(err.Error(), "main.go") {
		t.Fatalf("apply over dirty file: %v", err)
	}
	testrepo.Git(t, dir, "checkout", "--", "main.go")

	// drop the newer one: wip shifts to stash@{0} but is still found by sha
	if err := r.StashDrop(list[0].Sha); err != nil {
		t.Fatal(err)
	}
	if err := r.StashApply(wip.Sha, true); err != nil {
		t.Fatal(err)
	}
	if left, _ := r.Stashes(); len(left) != 0 {
		t.Fatalf("pop left stashes: %+v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, "nový soubor.txt")); err != nil {
		t.Error("untracked file not restored by pop")
	}
	if err := r.StashDrop(wip.Sha); err == nil {
		t.Error("dropping a missing stash must fail")
	}
}
