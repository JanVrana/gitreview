package git

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Write operations started from the UI. None of them ever forces anything:
// commits are refused when HEAD moved since the user looked, pulls are
// fast-forward only, pushes are never forced and stashes are only applied
// when they cannot clash with uncommitted work.

const writeTimeout = 2 * time.Minute // commit hooks and network operations may be slow

// ErrHeadMoved is returned by Commit when HEAD is not the commit the user
// reviewed (someone else, e.g. Claude Code, committed in the meantime).
var ErrHeadMoved = errors.New("HEAD has changed in the meantime – review the changes again")

// Pending is a file with changes not yet committed (staged, unstaged or
// untracked), as reported by `git status`.
// Renames are reported as a deletion plus an addition (--no-renames), so
// each path can be committed or reverted on its own.
type Pending struct {
	Path      string
	Untracked bool
}

// Uncommitted returns the files that differ from HEAD, keyed by path.
func (r *Repo) Uncommitted() (map[string]Pending, error) {
	out, err := r.run("status", "--porcelain=v1", "-z", "-uall", "--no-renames")
	if err != nil {
		return nil, err
	}
	res := map[string]Pending{}
	for _, e := range splitZ(out) {
		if len(e) < 4 {
			continue
		}
		p := Pending{Path: e[3:], Untracked: e[:2] == "??"}
		res[p.Path] = p
	}
	return res, nil
}

// Commit commits exactly the given paths (their working tree state, new
// files included) with message msg. Other staged changes stay staged and are
// not committed. expectHead must be the current HEAD.
func (r *Repo) Commit(paths []string, msg, expectHead string) (string, error) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "", errors.New("the commit message is empty")
	}
	if len(paths) == 0 {
		return "", errors.New("no files selected")
	}
	if h := r.Head(); h.Sha != expectHead {
		return "", ErrHeadMoved
	}
	pending, err := r.Uncommitted()
	if err != nil {
		return "", err
	}
	var untracked []string
	for _, p := range paths {
		f, ok := pending[p]
		if !ok || !SafePath(p) {
			return "", fmt.Errorf("%q has no uncommitted changes", p)
		}
		if f.Untracked {
			untracked = append(untracked, p)
		}
	}
	if len(untracked) > 0 {
		if _, err := r.runTimeout(writeTimeout, append([]string{"add", "--"}, untracked...)...); err != nil {
			return "", err
		}
	}
	args := append([]string{"commit", "--only", "--quiet", "-m", msg, "--"}, paths...)
	if _, err := r.runTimeout(writeTimeout, args...); err != nil {
		if len(untracked) > 0 { // leave the index as it was
			r.run(append([]string{"rm", "--cached", "--quiet", "--ignore-unmatch", "--"}, untracked...)...)
		}
		return "", err
	}
	return r.Head().Sha, nil
}

// inHead reports whether path exists in HEAD.
func (r *Repo) inHead(p string) bool {
	_, err := r.run("cat-file", "-e", "HEAD:"+p)
	return err == nil
}

// Revert discards all uncommitted changes of one file: a file known to HEAD
// is restored (index and working tree), a new file is deleted.
func (r *Repo) Revert(path string) error {
	pending, err := r.Uncommitted()
	if err != nil {
		return err
	}
	f, ok := pending[path]
	if !ok {
		return fmt.Errorf("%q has no uncommitted changes", path)
	}
	full, err := r.worktreePath(path)
	if err != nil {
		return err
	}
	if r.inHead(path) {
		_, err := r.run("restore", "--source=HEAD", "--staged", "--worktree", "--", path)
		return err
	}
	if !f.Untracked { // added to the index only
		if _, err := r.run("rm", "--cached", "--quiet", "--", path); err != nil {
			return err
		}
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// configuredUpstream returns the upstream set for branch ("" if none).
func (r *Repo) configuredUpstream(branch string) string {
	out, err := r.run("rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// PushResult describes a push of the current branch.
type PushResult struct {
	Branch   string `json:"branch"`
	Upstream string `json:"upstream"`
	Commits  int    `json:"commits"` // commits that were not on the upstream before
	Created  bool   `json:"created"` // the remote branch was created (-u)
}

func (r *Repo) currentBranch() (string, error) {
	h := r.Head()
	if h.Branch == "" || !ValidRef(h.Branch) {
		return "", errors.New("HEAD is detached – check out a branch first")
	}
	return h.Branch, nil
}

// Push pushes the current branch to its upstream, or creates origin/<branch>
// and sets it as upstream when the branch has none. It never forces.
func (r *Repo) Push() (PushResult, error) {
	branch, err := r.currentBranch()
	if err != nil {
		return PushResult{}, err
	}
	res := PushResult{Branch: branch, Upstream: r.configuredUpstream(branch)}
	if res.Upstream != "" {
		res.Commits, _, _ = r.AheadBehind(branch, res.Upstream)
		_, err = r.runTimeout(writeTimeout, "push", "--quiet")
		return res, err
	}
	if _, err := r.run("remote", "get-url", "origin"); err != nil {
		return res, errors.New("the branch has no upstream and there is no remote named origin")
	}
	res.Created = true
	res.Upstream = "origin/" + branch
	if base, err := r.run("rev-list", "--count", branch, "--not", "--remotes=origin"); err == nil {
		res.Commits, _ = strconv.Atoi(strings.TrimSpace(string(base)))
	}
	_, err = r.runTimeout(writeTimeout, "push", "--quiet", "-u", "origin", "refs/heads/"+branch+":refs/heads/"+branch)
	return res, err
}

// Pull fast-forwards the current branch to its upstream (`git pull --ff-only`).
// Git refuses when that would overwrite uncommitted changes.
func (r *Repo) Pull() (BranchUpdate, error) {
	branch, err := r.currentBranch()
	if err != nil {
		return BranchUpdate{}, err
	}
	u := BranchUpdate{Branch: branch, Upstream: r.configuredUpstream(branch)}
	if u.Upstream == "" {
		u.Status = NoUpstream
		return u, errors.New("the branch has no upstream – push it first")
	}
	from := r.Head().Sha
	if _, err := r.runTimeout(writeTimeout, "pull", "--ff-only", "--no-rebase", "--quiet"); err != nil {
		return u, err
	}
	to := r.Head().Sha
	u.From, u.To, u.Status = ShortSha(from), ShortSha(to), UpToDate
	if from != to {
		u.Status = Updated
		_, u.Commits, _ = r.AheadBehind(from, to)
	}
	return u, nil
}

// ---- stash ------------------------------------------------------------------------

// Stash is one entry of `git stash list`.
type Stash struct {
	Sha       string `json:"sha"`
	Ref       string `json:"ref"` // stash@{n}
	Time      int64  `json:"time"`
	Message   string `json:"message"`
	Untracked bool   `json:"untracked"` // stashed with -u: untracked files in sha^3
}

// Stashes lists the stash entries, newest first.
func (r *Repo) Stashes() ([]Stash, error) {
	out, err := r.run("stash", "list", "--format=%H%x1f%gd%x1f%ct%x1f%gs")
	if err != nil {
		return nil, err
	}
	res := []Stash{}
	for l := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(l, "\x1f")
		if len(f) != 4 {
			continue
		}
		s := Stash{Sha: f[0], Ref: f[1], Message: f[3]}
		s.Time, _ = strconv.ParseInt(f[2], 10, 64)
		_, err := r.run("rev-parse", "--verify", "--quiet", s.Sha+"^3")
		s.Untracked = err == nil
		res = append(res, s)
	}
	return res, nil
}

// FindStash returns the stash entry with the given commit sha. Looking it up
// by sha (not stash@{n}) keeps actions on the right entry when the list shifts.
func (r *Repo) FindStash(sha string) (Stash, error) {
	list, err := r.Stashes()
	if err != nil {
		return Stash{}, err
	}
	for _, s := range list {
		if s.Sha == sha {
			return s, nil
		}
	}
	return Stash{}, fmt.Errorf("stash %s does not exist", ShortSha(sha))
}

// StashFiles lists the files saved in a stash: tracked changes against the
// commit it was made on, plus untracked files (stash -u) as added files whose
// content comes from the Src commit.
func (r *Repo) StashFiles(s Stash) ([]FileChange, error) {
	files, err := r.ChangedFiles(s.Sha+"^1", s.Sha)
	if err != nil {
		return nil, err
	}
	if !s.Untracked {
		return files, nil
	}
	empty, err := r.run("hash-object", "-t", "tree", "/dev/null")
	if err != nil {
		return nil, err
	}
	src, err := r.Verify(s.Sha + "^3")
	if err != nil {
		return nil, err
	}
	untracked, err := r.ChangedFiles(strings.TrimSpace(string(empty)), src)
	if err != nil {
		return nil, err
	}
	for i := range untracked {
		untracked[i].Src = src
	}
	return append(files, untracked...), nil
}

// StashApply applies (and with pop also drops) a stash. It refuses when any
// file of the stash has uncommitted changes, so it cannot end in conflicts
// mixed into work in progress.
func (r *Repo) StashApply(sha string, pop bool) error {
	s, err := r.FindStash(sha)
	if err != nil {
		return err
	}
	files, err := r.StashFiles(s)
	if err != nil {
		return err
	}
	pending, err := r.Uncommitted()
	if err != nil {
		return err
	}
	var clash []string
	for _, f := range files {
		for _, p := range []string{f.Path, f.OldPath} {
			if _, ok := pending[p]; ok && p != "" && !slices.Contains(clash, p) {
				clash = append(clash, p)
			}
		}
	}
	if len(clash) > 0 {
		return fmt.Errorf("these files have uncommitted changes, commit or revert them first: %s", strings.Join(clash, ", "))
	}
	cmd := "apply"
	if pop {
		cmd = "pop"
	}
	_, err = r.runTimeout(writeTimeout, "stash", cmd, "--quiet", s.Ref)
	return err
}

// StashDrop deletes a stash entry.
func (r *Repo) StashDrop(sha string) error {
	s, err := r.FindStash(sha)
	if err != nil {
		return err
	}
	_, err = r.run("stash", "drop", "--quiet", s.Ref)
	return err
}
