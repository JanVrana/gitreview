package git

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const fetchTimeout = 2 * time.Minute

// Fetch downloads all remotes and prunes deleted remote branches. It never
// touches the working tree or local branches.
func (r *Repo) Fetch() error {
	_, err := r.runTimeout(fetchTimeout, "fetch", "--all", "--prune", "--no-recurse-submodules", "--quiet")
	return err
}

// Upstream returns the remote-tracking branch of a local branch: its
// configured upstream, else origin/<branch> when that exists, else "".
func (r *Repo) Upstream(branch string) string {
	if !ValidRef(branch) {
		return ""
	}
	// no --end-of-options: some git versions echo it here; ValidRef rules out a leading "-"
	if out, err := r.run("rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}"); err == nil {
		if up := strings.TrimSpace(string(out)); up != "" {
			return up
		}
	}
	if _, err := r.run("rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch); err == nil {
		return "origin/" + branch
	}
	return ""
}

// AheadBehind counts the commits only in a (ahead) and only in b (behind).
func (r *Repo) AheadBehind(a, b string) (ahead, behind int, err error) {
	out, err := r.run("rev-list", "--left-right", "--count", "--end-of-options", a+"..."+b)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", out)
	}
	ahead, _ = strconv.Atoi(f[0])
	behind, _ = strconv.Atoi(f[1])
	return ahead, behind, nil
}

// LocalBranch reports whether refs/heads/<name> exists.
func (r *Repo) LocalBranch(name string) bool {
	if !ValidRef(name) {
		return false
	}
	_, err := r.run("rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// Update statuses returned by FastForward.
const (
	Updated    = "updated"    // moved forward to the upstream
	UpToDate   = "uptodate"   // already at (or ahead of) the upstream
	Diverged   = "diverged"   // has local commits the upstream lacks; left alone
	CheckedOut = "checkedout" // checked out in a worktree; left alone
	NoUpstream = "noupstream" // no remote-tracking branch to follow
)

// BranchUpdate describes what FastForward did with one branch.
type BranchUpdate struct {
	Branch   string `json:"branch"`
	Upstream string `json:"upstream,omitempty"`
	Status   string `json:"status"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Commits  int    `json:"commits,omitempty"` // commits behind the upstream (pulled in when Updated)
	Ahead    int    `json:"ahead,omitempty"`
}

// FastForward moves a local branch to its upstream when that is a pure
// fast-forward. The checked-out branch is never moved (its working tree would
// go out of sync); that is the user's `git pull`.
func (r *Repo) FastForward(branch string) (BranchUpdate, error) {
	u := BranchUpdate{Branch: branch, Upstream: r.Upstream(branch)}
	if u.Upstream == "" {
		u.Status = NoUpstream
		return u, nil
	}
	from, err := r.Verify("refs/heads/" + branch)
	if err != nil {
		return u, err
	}
	to, err := r.Verify(u.Upstream)
	if err != nil {
		return u, err
	}
	u.From, u.To = ShortSha(from), ShortSha(to)
	if u.Ahead, u.Commits, err = r.AheadBehind(from, to); err != nil {
		return u, err
	}
	switch {
	case u.Commits == 0:
		u.Status = UpToDate
	case u.Ahead > 0:
		u.Status = Diverged
	case r.checkedOut(branch):
		u.Status = CheckedOut
	default:
		// compare-and-swap: fails if the branch moved since we looked
		if _, err := r.run("update-ref", "-m", "gitreview: fast-forward to "+u.Upstream,
			"refs/heads/"+branch, to, from); err != nil {
			return u, err
		}
		u.Status = Updated
	}
	return u, nil
}

// checkedOut reports whether branch is the HEAD of any worktree.
func (r *Repo) checkedOut(branch string) bool {
	out, err := r.run("worktree", "list", "--porcelain")
	if err != nil {
		return true // be safe
	}
	for l := range strings.SplitSeq(string(out), "\n") {
		if l == "branch refs/heads/"+branch {
			return true
		}
	}
	return false
}
