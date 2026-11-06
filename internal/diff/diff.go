// Package diff turns `git diff -U0` output into side-by-side blocks that
// cover both files completely.
package diff

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
)

// Hunk is a single @@ header from unified diff output (1-based, git semantics).
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
}

// Block is a contiguous region of both files. Starts are 0-based line indexes.
type Block struct {
	Type       string `json:"type"` // "equal" or "change"
	LeftStart  int    `json:"leftStart"`
	LeftLen    int    `json:"leftLen"`
	RightStart int    `json:"rightStart"`
	RightLen   int    `json:"rightLen"`
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseHunks extracts hunk headers from unified diff output.
func ParseHunks(out []byte) []Hunk {
	var res []Hunk
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, []byte("@@ ")) {
			continue
		}
		m := hunkRe.FindSubmatch(line)
		if m == nil {
			continue
		}
		res = append(res, Hunk{
			OldStart: atoi(m[1]), OldLines: count(m[2]),
			NewStart: atoi(m[3]), NewLines: count(m[4]),
		})
	}
	return res
}

func atoi(b []byte) int { n, _ := strconv.Atoi(string(b)); return n }

// count parses an optional hunk length; git omits it when it equals 1.
func count(b []byte) int {
	if len(b) == 0 {
		return 1
	}
	return atoi(b)
}

// start converts a 1-based hunk start to a 0-based index. For an empty side
// git reports the line *after which* the change happens, which already is
// the 0-based insertion index.
func start(s, n int) int {
	if n == 0 {
		return s
	}
	return s - 1
}

// Blocks builds blocks covering leftCount and rightCount lines from hunks.
// It never returns nil.
func Blocks(hunks []Hunk, leftCount, rightCount int) []Block {
	res := []Block{}
	l, r := 0, 0
	equal := func(n int) {
		if n > 0 {
			res = append(res, Block{Type: "equal", LeftStart: l, LeftLen: n, RightStart: r, RightLen: n})
			l += n
			r += n
		}
	}
	change := func(ll, rl int) {
		if ll > 0 || rl > 0 {
			res = append(res, Block{Type: "change", LeftStart: l, LeftLen: ll, RightStart: r, RightLen: rl})
			l += ll
			r += rl
		}
	}
	for _, h := range hunks {
		ls, rs := start(h.OldStart, h.OldLines), start(h.NewStart, h.NewLines)
		if ls < l || rs < r || ls+h.OldLines > leftCount || rs+h.NewLines > rightCount {
			// Hunks do not match the content (the file changed between the
			// calls). Never claim the rest is unchanged: show it as a change.
			change(leftCount-l, rightCount-r)
			return res
		}
		// Gaps before a hunk are equal on both sides; if git ever disagrees,
		// show the surplus as a change rather than misalign the rest.
		equal(min(ls-l, rs-r))
		change(ls-l, rs-r)
		change(h.OldLines, h.NewLines)
	}
	if leftCount-l == rightCount-r {
		equal(leftCount - l)
	} else {
		change(leftCount-l, rightCount-r)
	}
	return res
}

// Whole describes a file that exists on one side only (or is empty on both).
func Whole(leftLen, rightLen int) []Block {
	if leftLen == 0 && rightLen == 0 {
		return []Block{}
	}
	return []Block{{Type: "change", LeftLen: leftLen, RightLen: rightLen}}
}
