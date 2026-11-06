// Package git wraps the git CLI for inspecting a repository. The only
// writes are fetching and fast-forwarding base branches (remote.go).
//
// Every call runs without a shell, with --no-optional-locks (polling never
// takes the index lock while another process such as Claude Code commits in
// the same repository) and with --literal-pathspecs (file names like "*.txt"
// are not globs).
package git

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MaxTextBytes is the size above which file contents are not loaded
// automatically (untracked files are not line-counted, diffs need "force").
const MaxTextBytes = 1 << 20

const cmdTimeout = 30 * time.Second

// Repo is a git working tree.
type Repo struct {
	Root string
}

// Open locates the top-level directory of the repository containing path.
func Open(path string) (*Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	r := &Repo{Root: abs}
	out, err := r.run("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not a git repository: %w", abs, err)
	}
	r.Root = strings.TrimSpace(string(out))
	return r, nil
}

func (r *Repo) run(args ...string) ([]byte, error) {
	return r.runTimeout(cmdTimeout, args...)
}

func (r *Repo) runTimeout(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	base := []string{
		"-C", r.Root,
		"--no-optional-locks", "--no-pager", "--literal-pathspecs",
		"-c", "color.ui=never",
		"-c", "core.quotepath=false",
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	detach(cmd) // no controlling terminal: ssh cannot prompt for a passphrase and hang
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

var refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@{}^~\-]*$`)

// ValidRef reports whether s is syntactically safe to pass to git as a revision.
func ValidRef(s string) bool {
	return s != "" && len(s) < 256 && refPattern.MatchString(s) && !strings.Contains(s, "..")
}

// Verify resolves a revision to a full commit sha.
func (r *Repo) Verify(ref string) (string, error) {
	if !ValidRef(ref) {
		return "", fmt.Errorf("invalid ref %q", ref)
	}
	out, err := r.run("rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("ref %q does not exist", ref)
	}
	return strings.TrimSpace(string(out)), nil
}

// Head describes the currently checked-out state.
type Head struct {
	Branch   string `json:"branch"` // empty when detached
	Detached bool   `json:"detached"`
	Sha      string `json:"sha"` // empty in a repository without commits
}

// Head returns the current branch and commit.
func (r *Repo) Head() Head {
	var h Head
	if out, err := r.run("symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		h.Branch = strings.TrimSpace(string(out))
	} else {
		h.Detached = true
	}
	if out, err := r.run("rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		h.Sha = strings.TrimSpace(string(out))
	}
	return h
}

// ResolveBase finds a base branch locally, falling back to origin/<name>.
// It returns the ref name actually used.
func (r *Repo) ResolveBase(name string) (string, error) {
	if !ValidRef(name) {
		return "", fmt.Errorf("invalid ref %q", name)
	}
	for _, cand := range []string{"refs/heads/" + name, "refs/remotes/origin/" + name} {
		if _, err := r.run("rev-parse", "--verify", "--quiet", cand); err == nil {
			return strings.TrimPrefix(strings.TrimPrefix(cand, "refs/heads/"), "refs/remotes/"), nil
		}
	}
	if _, err := r.Verify(name); err == nil {
		return name, nil
	}
	return "", fmt.Errorf("branch %q does not exist (nor origin/%s)", name, name)
}

// Branches lists local and remote branch names.
func (r *Repo) Branches() []string {
	out, err := r.run("for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil
	}
	var res []string
	for l := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if l != "" && !strings.HasSuffix(l, "/HEAD") && l != "origin" {
			res = append(res, l)
		}
	}
	return res
}

// MergeBase returns the best common ancestor of a and b.
func (r *Repo) MergeBase(a, b string) (string, error) {
	out, err := r.run("merge-base", a, b)
	if err != nil {
		return "", errors.New("the branches have no common ancestor")
	}
	return strings.TrimSpace(string(out)), nil
}

// ShortSha returns an abbreviated commit id.
func ShortSha(sha string) string {
	return sha[:min(len(sha), 8)]
}

// FileChange is one entry of the changed-file list.
type FileChange struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Status  string `json:"status"` // M, A, D, R, T, U (untracked)
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary"`
	Sig     string `json:"sig"` // changes whenever the right-hand content changes
}

const zeroSha = "0000000000000000000000000000000000000000"

// revArgs returns the revision arguments for a diff of left against right
// (a commit) or the working tree (right == "").
func revArgs(left, right string) []string {
	if right == "" {
		return []string{left}
	}
	return []string{left, right}
}

// ChangedFiles lists files differing between left (a commit) and right
// (a commit, or "" for the working tree including untracked files).
// It never returns a nil slice without an error.
func (r *Repo) ChangedFiles(left, right string) ([]FileChange, error) {
	raw, err := r.run(append([]string{"diff", "--raw", "-z", "-M", "--no-abbrev"}, revArgs(left, right)...)...)
	if err != nil {
		return nil, err
	}
	files := parseRaw(raw)
	if ns, err := r.run(append([]string{"diff", "--numstat", "-z", "-M"}, revArgs(left, right)...)...); err == nil {
		applyNumstat(files, ns)
	}
	if right != "" {
		return files, nil
	}

	for i := range files {
		if files[i].Sig == "" {
			files[i].Sig = r.statSig(files[i].Path)
		}
	}
	out, err := r.run("ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return files, nil
	}
	for _, p := range splitZ(out) {
		fc := FileChange{Path: p, Status: "U", Sig: r.statSig(p)}
		fc.Added, fc.Binary = r.countUntracked(p)
		files = append(files, fc)
	}
	return files, nil
}

// countUntracked returns the line count of an untracked file without
// loading large files: big files report 0 lines, binary detection reads
// only the first 8 KB.
func (r *Repo) countUntracked(p string) (lines int, binary bool) {
	full, err := r.worktreePath(p)
	if err != nil {
		return 0, false
	}
	st, err := os.Lstat(full)
	if err != nil || !st.Mode().IsRegular() {
		return 0, false
	}
	f, err := os.Open(full)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	head := make([]byte, 8000)
	n, _ := io.ReadFull(f, head)
	if IsBinary(head[:n]) {
		return 0, true
	}
	if st.Size() > MaxTextBytes {
		return 0, false
	}
	rest, _ := io.ReadAll(f)
	return len(SplitLines(append(head[:n], rest...))), false
}

func splitZ(b []byte) []string {
	var res []string
	for p := range strings.SplitSeq(string(b), "\x00") {
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}

// parseRaw parses `git diff --raw -z` output:
// ":<mode> <mode> <sha> <sha> <status>\0<path>\0[<path>\0]"
func parseRaw(b []byte) []FileChange {
	parts := strings.Split(string(b), "\x00")
	res := []FileChange{}
	for i := 0; i < len(parts); i++ {
		meta := parts[i]
		if !strings.HasPrefix(meta, ":") {
			continue
		}
		f := strings.Fields(meta[1:])
		if len(f) < 5 || i+1 >= len(parts) {
			continue
		}
		fc := FileChange{Status: f[4][:1]}
		if f[3] != zeroSha {
			fc.Sig = f[3]
		}
		if fc.Status == "R" || fc.Status == "C" {
			if i+2 >= len(parts) {
				break
			}
			fc.OldPath, fc.Path = parts[i+1], parts[i+2]
			i += 2
			if fc.Status == "C" {
				fc.Status = "A"
				fc.OldPath = ""
			}
		} else {
			fc.Path = parts[i+1]
			i++
		}
		res = append(res, fc)
	}
	return res
}

// applyNumstat merges `git diff --numstat -z` counts into files.
// Normal entry: "<add>\t<del>\t<path>\0"; rename: "<add>\t<del>\t\0<old>\0<new>\0".
func applyNumstat(files []FileChange, b []byte) {
	idx := map[string]*FileChange{}
	for i := range files {
		idx[files[i].Path] = &files[i]
	}
	parts := strings.Split(string(b), "\x00")
	for i := 0; i < len(parts); i++ {
		f := strings.SplitN(parts[i], "\t", 3)
		if len(f) != 3 {
			continue
		}
		path := f[2]
		if path == "" && i+2 < len(parts) {
			path = parts[i+2]
			i += 2
		}
		fc, ok := idx[path]
		if !ok {
			continue
		}
		if f[0] == "-" {
			fc.Binary = true
			continue
		}
		fc.Added, _ = strconv.Atoi(f[0])
		fc.Deleted, _ = strconv.Atoi(f[1])
	}
}

func (r *Repo) statSig(p string) string {
	st, err := os.Lstat(filepath.Join(r.Root, filepath.FromSlash(p)))
	if err != nil {
		return "missing"
	}
	return fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size())
}

// SafePath checks that a repository-relative path stays inside the repository.
func SafePath(p string) bool {
	if p == "" || strings.ContainsRune(p, 0) || filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	return clean == p && clean != ".." && !strings.HasPrefix(clean, "../") && !strings.HasPrefix(clean, ".git/")
}

func (r *Repo) worktreePath(p string) (string, error) {
	if !SafePath(p) {
		return "", fmt.Errorf("invalid path %q", p)
	}
	return filepath.Join(r.Root, filepath.FromSlash(p)), nil
}

// ErrNotExist is returned when a file does not exist in the requested revision.
var ErrNotExist = errors.New("file does not exist in this revision")

// Content returns a file's content at rev, or from the working tree when rev
// is "". Symlinks are not followed; their target text is returned, which is
// also what git diffs. A missing file yields ErrNotExist; any other failure
// is reported as a regular error.
func (r *Repo) Content(rev, p string) ([]byte, error) {
	if rev == "" {
		full, err := r.worktreePath(p)
		if err != nil {
			return nil, err
		}
		st, err := os.Lstat(full)
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotExist
		}
		if err != nil {
			return nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			t, err := os.Readlink(full)
			return []byte(t), err
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("%q is not a regular file", p)
		}
		return os.ReadFile(full)
	}
	if !SafePath(p) {
		return nil, fmt.Errorf("invalid path %q", p)
	}
	if _, err := r.run("rev-parse", "--verify", "--quiet", rev+":"+p); err != nil {
		return nil, ErrNotExist
	}
	return r.run("cat-file", "blob", rev+":"+p)
}

// Hunks returns the raw `git diff -U0` output for one file.
func (r *Repo) Hunks(left, right, oldPath, path string, ignoreWS bool) ([]byte, error) {
	args := []string{"diff", "-U0", "--no-ext-diff", "--no-textconv", "--diff-algorithm=myers", "-M"}
	if ignoreWS {
		args = append(args, "-w")
	}
	args = append(append(args, revArgs(left, right)...), "--", path)
	if oldPath != "" && oldPath != path {
		args = append(args, oldPath)
	}
	return r.run(args...)
}

// Fingerprint changes whenever HEAD, branch tips, or the working tree change.
// It fails when any git call fails, so callers can skip transient errors
// (e.g. while another process holds a lock) instead of reporting a change.
func (r *Repo) Fingerprint() (string, error) {
	h := sha1.New()
	for _, args := range [][]string{
		{"rev-parse", "HEAD"},
		{"for-each-ref", "--format=%(objectname) %(refname)", "refs/heads", "refs/remotes"},
	} {
		out, err := r.run(args...)
		if err != nil {
			return "", err
		}
		h.Write(out)
	}
	out, err := r.run("status", "--porcelain=v1", "-z", "-uall")
	if err != nil {
		return "", err
	}
	h.Write(out)
	parts := splitZ(out)
	for i := 0; i < len(parts); i++ {
		e := parts[i]
		if len(e) < 4 {
			continue
		}
		h.Write([]byte(r.statSig(e[3:])))
		if strings.ContainsAny(e[:2], "RC") {
			i++ // the next entry is the original path of a rename/copy
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// GitPath resolves a path inside the .git directory (worktree aware).
func (r *Repo) GitPath(name string) (string, error) {
	out, err := r.run("rev-parse", "--git-path", name)
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.Root, p)
	}
	return p, nil
}

// IsBinary reports whether content looks binary (NUL in the first 8 KB).
func IsBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}

// SplitLines splits text into lines the way git counts them: a trailing
// newline does not produce an extra empty line, and CR of CRLF is dropped.
func SplitLines(b []byte) []string {
	if len(b) == 0 {
		return []string{}
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}
