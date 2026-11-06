// Package testrepo builds throwaway git repositories for tests.
package testrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Git runs git in dir and fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// Write creates a file (and parent dirs) in dir.
func Write(t testing.TB, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Lines returns n numbered lines.
func Lines(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(prefix)
		b.WriteString(" line ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("\n")
	}
	return b.String()
}

// New creates a repository with a develop branch and a feature branch:
//
//	develop: main.go, rename-me.txt, delete-me.txt, bin.dat
//	feature (committed): main.go modified, rename-me.txt -> renamed.txt, delete-me.txt removed
//	worktree: main.go further modified, untracked "nový soubor.txt", binary change
func New(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", "develop")
	Write(t, dir, "main.go", Lines("main", 30))
	Write(t, dir, "rename-me.txt", Lines("ren", 20))
	Write(t, dir, "delete-me.txt", "bye\n")
	Write(t, dir, "bin.dat", "\x00\x01\x02")
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-q", "-m", "base")
	Git(t, dir, "branch", "master")
	Git(t, dir, "checkout", "-q", "-b", "feature")

	lines := strings.Split(Lines("main", 30), "\n")
	lines[4] = "main CHANGED line"
	lines = append(lines[:10], append([]string{"inserted 1", "inserted 2"}, lines[10:]...)...)
	Write(t, dir, "main.go", strings.Join(lines, "\n"))
	Git(t, dir, "mv", "rename-me.txt", "renamed.txt")
	Git(t, dir, "rm", "-q", "delete-me.txt")
	Git(t, dir, "commit", "-q", "-am", "feature work")

	// uncommitted changes
	lines = append(lines[:20], lines[23:]...) // delete 3 lines
	Write(t, dir, "main.go", strings.Join(lines, "\n"))
	Write(t, dir, "nový soubor.txt", "ahoj\nsvěte\n")
	Write(t, dir, "bin.dat", "\x00\x01\x03")
	return dir
}
