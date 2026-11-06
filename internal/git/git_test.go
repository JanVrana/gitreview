package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JanVrana/gitreview/internal/diff"
	"github.com/JanVrana/gitreview/internal/testrepo"
)

func files(t *testing.T, r *Repo, left, right string) map[string]FileChange {
	t.Helper()
	fs, err := r.ChangedFiles(left, right)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]FileChange{}
	for _, f := range fs {
		m[f.Path] = f
	}
	return m
}

func TestChangedFilesWorktreeAndHead(t *testing.T) {
	dir := testrepo.New(t)
	r, err := Open(filepath.Join(dir)) // also works from repo root
	if err != nil {
		t.Fatal(err)
	}
	base, err := r.ResolveBase("develop")
	if err != nil || base != "develop" {
		t.Fatalf("resolve: %q %v", base, err)
	}
	baseSha, _ := r.Verify(base)
	h := r.Head()
	if h.Branch != "feature" || h.Detached || h.Sha == "" {
		t.Fatalf("head: %+v", h)
	}
	mb, err := r.MergeBase(baseSha, h.Sha)
	if err != nil || mb != baseSha {
		t.Fatalf("merge-base: %s %v", mb, err)
	}

	wt := files(t, r, mb, "")
	if f := wt["main.go"]; f.Status != "M" || f.Added == 0 || f.Deleted == 0 || f.Sig == "" {
		t.Errorf("main.go: %+v", f)
	}
	if f := wt["renamed.txt"]; f.Status != "R" || f.OldPath != "rename-me.txt" {
		t.Errorf("rename: %+v", f)
	}
	if f := wt["delete-me.txt"]; f.Status != "D" {
		t.Errorf("delete: %+v", f)
	}
	if f := wt["nový soubor.txt"]; f.Status != "U" || f.Added != 2 {
		t.Errorf("untracked with diacritics: %+v", f)
	}
	if f := wt["bin.dat"]; !f.Binary {
		t.Errorf("binary: %+v", f)
	}

	hd := files(t, r, mb, h.Sha)
	if _, ok := hd["nový soubor.txt"]; ok {
		t.Error("untracked must not appear when comparing commits")
	}
	if _, ok := hd["bin.dat"]; ok {
		t.Error("uncommitted binary change must not appear when comparing commits")
	}
	if hd["main.go"].Sig == wt["main.go"].Sig {
		t.Error("sig should differ between HEAD and worktree content")
	}
}

func TestHunksMatchContent(t *testing.T) {
	dir := testrepo.New(t)
	r, _ := Open(dir)
	baseSha, _ := r.Verify("develop")
	wantChanges := map[string]int{"": 3, r.Head().Sha: 2}
	for _, right := range []string{"", r.Head().Sha} {
		left, _ := r.Content(baseSha, "main.go")
		rightData, err := r.Content(right, "main.go")
		if err != nil {
			t.Fatal(err)
		}
		out, err := r.Hunks(baseSha, right, "", "main.go", false)
		if err != nil {
			t.Fatal(err)
		}
		ll, rl := SplitLines(left), SplitLines(rightData)
		bs := diff.Blocks(diff.ParseHunks(out), len(ll), len(rl))
		// every equal block must really contain equal lines
		changes := 0
		for _, b := range bs {
			if b.Type == "change" {
				changes++
				continue
			}
			for k := 0; k < b.LeftLen; k++ {
				if ll[b.LeftStart+k] != rl[b.RightStart+k] {
					t.Fatalf("right=%q: equal block %v differs at %d: %q vs %q", right, b, k, ll[b.LeftStart+k], rl[b.RightStart+k])
				}
			}
		}
		if changes != wantChanges[right] {
			t.Fatalf("right=%q: %d change blocks, want %d: %v", right, changes, wantChanges[right], bs)
		}
	}
	// rename diff limited to both paths
	out, err := r.Hunks(baseSha, "", "rename-me.txt", "renamed.txt", false)
	if err != nil || len(diff.ParseHunks(out)) != 0 {
		t.Fatalf("pure rename should have no hunks: %v %s", err, out)
	}
}

func TestContentSafety(t *testing.T) {
	dir := testrepo.New(t)
	r, _ := Open(dir)
	for _, p := range []string{"../etc/passwd", "/etc/passwd", "a/../../x", ".git/config", "", "./main.go"} {
		if _, err := r.Content("", p); err == nil {
			t.Errorf("path %q must be rejected", p)
		}
	}
	// symlinks are returned as their target, never followed
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0o644)
	os.Symlink(outside, filepath.Join(dir, "link"))
	data, err := r.Content("", "link")
	if err != nil || string(data) != outside {
		t.Fatalf("symlink: %q %v", data, err)
	}
	if _, err := r.Content("", "missing.txt"); err != ErrNotExist {
		t.Fatalf("missing: %v", err)
	}
}

func TestRefsAndFallback(t *testing.T) {
	dir := testrepo.New(t)
	r, _ := Open(dir)
	for _, bad := range []string{"--output=/tmp/x", "a..b", "-x", "a b", "x;rm"} {
		if ValidRef(bad) {
			t.Errorf("ref %q must be invalid", bad)
		}
	}
	if _, err := r.ResolveBase("nonexistent"); err == nil {
		t.Error("missing base must fail")
	}
	// only a remote-tracking branch exists
	testrepo.Git(t, dir, "update-ref", "refs/remotes/origin/release", "develop")
	if ref, err := r.ResolveBase("release"); err != nil || ref != "origin/release" {
		t.Fatalf("fallback: %q %v", ref, err)
	}
}

func TestFingerprintChanges(t *testing.T) {
	dir := testrepo.New(t)
	r, _ := Open(dir)
	a, err := r.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := r.Fingerprint(); a != b {
		t.Fatal("fingerprint not stable")
	}
	// modifying an already-modified file must change the fingerprint
	testrepo.Write(t, dir, "main.go", "different content entirely\n")
	if b, _ := r.Fingerprint(); b == a {
		t.Fatal("fingerprint did not change")
	}
}

func TestSplitLines(t *testing.T) {
	cases := map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\r\nb\r\n": 2, "\n": 1}
	for in, n := range cases {
		if got := SplitLines([]byte(in)); len(got) != n {
			t.Errorf("%q: %d lines, want %d", in, len(got), n)
		}
	}
}

func TestLiteralPathspecs(t *testing.T) {
	dir := testrepo.New(t)
	r, _ := Open(dir)
	base, _ := r.Verify("develop")
	// a file literally named "*.txt" must not act as a glob matching renamed.txt
	testrepo.Write(t, dir, "*.txt", "glob\n")
	testrepo.Git(t, dir, "add", "*.txt")
	out, err := r.Hunks(base, "", "", "*.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "renamed.txt") || len(diff.ParseHunks(out)) != 1 {
		t.Fatalf("pathspec treated as glob:\n%s", out)
	}
}

func TestLargeUntrackedNotCounted(t *testing.T) {
	dir := testrepo.New(t)
	r, _ := Open(dir)
	base, _ := r.Verify("develop")
	testrepo.Write(t, dir, "big.log", strings.Repeat("x\n", MaxTextBytes))
	f := files(t, r, base, "")["big.log"]
	if f.Status != "U" || f.Added != 0 || f.Binary {
		t.Fatalf("big untracked: %+v", f)
	}
}

func TestContentErrors(t *testing.T) {
	dir := testrepo.New(t)
	r, _ := Open(dir)
	base, _ := r.Verify("develop")
	if _, err := r.Content(base, "renamed.txt"); err != ErrNotExist {
		t.Fatalf("missing in rev: %v", err)
	}
	os.Mkdir(filepath.Join(dir, "adir"), 0o755)
	if _, err := r.Content("", "adir"); err == nil || err == ErrNotExist {
		t.Fatalf("directory must be a real error, got %v", err)
	}
}
