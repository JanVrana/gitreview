package git

import (
	"fmt"
	"strconv"
	"strings"
)

// EmptyTree is git's empty tree: the "parent" of a root commit.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Commit is one entry of a branch log.
type Commit struct {
	Sha     string   `json:"sha"`
	Parents []string `json:"parents"`
	Author  string   `json:"author"`
	Time    int64    `json:"time"`
	Subject string   `json:"subject"`
}

// MaxLog is the most commits Log returns.
const MaxLog = 300

// Log lists the commits reachable from to but not from from, newest first,
// at most MaxLog of them. more tells whether there were more.
func (r *Repo) Log(from, to string) (commits []Commit, more bool, err error) {
	out, err := r.run("log", "-z", "--format=%H%x1f%P%x1f%an%x1f%at%x1f%s",
		"--max-count="+strconv.Itoa(MaxLog+1), "--end-of-options", from+".."+to)
	if err != nil {
		return nil, false, err
	}
	commits = []Commit{}
	for _, rec := range splitZ(out) {
		if c, ok := parseCommit(rec); ok {
			commits = append(commits, c)
		}
	}
	if len(commits) > MaxLog {
		return commits[:MaxLog], true, nil
	}
	return commits, false, nil
}

// FirstParent returns the commit a commit is compared with: its first parent,
// or the empty tree for a root commit.
func (r *Repo) FirstParent(sha string) string {
	if p, err := r.Verify(sha + "^1"); err == nil {
		return p
	}
	return EmptyTree
}

// CommitInfo describes one commit.
func (r *Repo) CommitInfo(sha string) (Commit, error) {
	out, err := r.run("log", "-1", "-z", "--format=%H%x1f%P%x1f%an%x1f%at%x1f%s", "--end-of-options", sha)
	if err != nil {
		return Commit{}, err
	}
	c, ok := parseCommit(string(out))
	if !ok {
		return Commit{}, fmt.Errorf("commit %s not found", sha)
	}
	return c, nil
}

func parseCommit(rec string) (Commit, bool) {
	f := strings.Split(strings.Trim(rec, "\n\x00"), "\x1f")
	if len(f) != 5 {
		return Commit{}, false
	}
	c := Commit{Sha: f[0], Parents: strings.Fields(f[1]), Author: f[2], Subject: f[4]}
	c.Time, _ = strconv.ParseInt(f[3], 10, 64)
	return c, true
}

// IsAncestor tells whether commit a is an ancestor of (or equal to) b.
func (r *Repo) IsAncestor(a, b string) bool {
	_, err := r.run("merge-base", "--is-ancestor", a, b)
	return err == nil
}
