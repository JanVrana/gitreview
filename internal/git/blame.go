package git

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// BlameCommit is a commit that last changed some lines. Lines not committed
// yet have an all-zero Sha.
type BlameCommit struct {
	Sha     string `json:"sha"`
	Author  string `json:"author"`
	Time    int64  `json:"time"`
	Summary string `json:"summary"`
}

// Blame tells for each line (0-based) which of Commits changed it last.
type Blame struct {
	Commits []BlameCommit `json:"commits"`
	Lines   []int         `json:"lines"`
}

var blameHeadRe = regexp.MustCompile(`^([0-9a-f]{40}(?:[0-9a-f]{24})?) \d+ (\d+)`)

// Blame annotates a file at rev, or in the working tree when rev is "".
func (r *Repo) Blame(rev, p string, ignoreWS bool) (*Blame, error) {
	if !SafePath(p) {
		return nil, ErrNotExist
	}
	args := []string{"blame", "--porcelain"}
	if ignoreWS {
		args = append(args, "-w")
	}
	if rev != "" {
		args = append(args, rev)
	}
	out, err := r.run(append(args, "--", p)...)
	if err != nil {
		return nil, err
	}
	return parseBlame(out), nil
}

func parseBlame(out []byte) *Blame {
	b := &Blame{Commits: []BlameCommit{}, Lines: []int{}}
	index := map[string]int{}
	cur := -1
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "\t") {
			continue // the line's content
		}
		if m := blameHeadRe.FindStringSubmatch(line); m != nil {
			i, ok := index[m[1]]
			if !ok {
				i = len(b.Commits)
				index[m[1]] = i
				b.Commits = append(b.Commits, BlameCommit{Sha: m[1]})
			}
			cur = i
			n, _ := strconv.Atoi(m[2])
			for len(b.Lines) < n {
				b.Lines = append(b.Lines, -1)
			}
			b.Lines[n-1] = i
			continue
		}
		if cur < 0 {
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		c := &b.Commits[cur]
		switch key {
		case "author":
			c.Author = val
		case "author-time":
			c.Time, _ = strconv.ParseInt(val, 10, 64)
		case "summary":
			c.Summary = val
		}
	}
	return b
}

// NotCommitted is the blame of a file nobody committed yet.
func NotCommitted(lines int) *Blame {
	return &Blame{Commits: []BlameCommit{{Sha: strings.Repeat("0", 40)}}, Lines: make([]int, lines)}
}
