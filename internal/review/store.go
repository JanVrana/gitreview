// Package review stores line comments made during review and exports them
// as a Markdown file that Claude Code can act on.
package review

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JanVrana/gitreview/internal/lang"
)

// Comment is anchored by file, side and line plus the line's text at the
// time it was written, so the UI can detect when the code moved under it.
type Comment struct {
	ID           string    `json:"id"`
	File         string    `json:"file"`
	Side         string    `json:"side"` // "left" (base) or "right" (changes)
	Line         int       `json:"line"` // 1-based
	LineText     string    `json:"lineText"`
	Context      []string  `json:"context"`      // snippet around the line
	ContextStart int       `json:"contextStart"` // 1-based line of Context[0]
	Text         string    `json:"text"`
	Created      time.Time `json:"created"`
}

// Store persists comments as JSON in a single file.
type Store struct {
	mu   sync.Mutex
	file string
}

// NewStore uses dir/comments.json (dir is created lazily).
func NewStore(dir string) *Store {
	return &Store{file: filepath.Join(dir, "comments.json")}
}

func (s *Store) load() ([]Comment, error) {
	data, err := os.ReadFile(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return []Comment{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cs []Comment
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("corrupted %s: %w", s.file, err)
	}
	return cs, nil
}

func (s *Store) save(cs []Comment) error {
	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.file, data)
}

func writeAtomic(file string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

// List returns all comments sorted by file, side and line.
func (s *Store) List() ([]Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := s.load()
	if err != nil {
		return nil, err
	}
	sortComments(cs)
	return cs, nil
}

func sortComments(cs []Comment) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Side != b.Side {
			return a.Side == "left"
		}
		return a.Line < b.Line
	})
}

// Validate checks a comment submitted by the client.
func (c *Comment) Validate() error {
	c.Text = strings.TrimSpace(c.Text)
	switch {
	case c.Text == "":
		return errors.New("empty comment")
	case c.File == "" || strings.ContainsAny(c.File, "\x00\n"):
		return errors.New("missing file")
	case c.Side != "left" && c.Side != "right":
		return errors.New("side must be left or right")
	case c.Line < 1:
		return errors.New("invalid line")
	case len(c.Text) > 64*1024 || len(c.Context) > 50:
		return errors.New("comment is too long")
	}
	return nil
}

// Add stores a new comment and returns it with ID and timestamp filled in.
func (s *Store) Add(c Comment) (Comment, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := s.load()
	if err != nil {
		return c, err
	}
	c.ID = newID()
	c.Created = time.Now()
	cs = append(cs, c)
	return c, s.save(cs)
}

// Update changes the text of a comment.
func (s *Store) Update(id, text string) (Comment, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Comment{}, errors.New("empty comment")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := s.load()
	if err != nil {
		return Comment{}, err
	}
	for i := range cs {
		if cs[i].ID == id {
			cs[i].Text = text
			return cs[i], s.save(cs)
		}
	}
	return Comment{}, errors.New("comment not found")
}

// Delete removes one comment.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := s.load()
	if err != nil {
		return err
	}
	out := cs[:0]
	for _, c := range cs {
		if c.ID != id {
			out = append(out, c)
		}
	}
	return s.save(out)
}

// Clear removes all comments.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save([]Comment{})
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ExportInfo describes the compared revisions for the Markdown header.
type ExportInfo struct {
	Branch    string
	LeftDesc  string
	RightDesc string
	Lang      string // "cs" or "en" (default)
}

// exportText holds the translatable parts of REVIEW.md.
type exportText struct {
	title, intro, branch, count, sideRight, sideLeft string
}

var exportTexts = map[string]exportText{
	"en": {
		title: "# Code review – comments\n\n",
		intro: "Please address the following code review comments. " +
			"Each one has a file, a line number, a code snippet (the commented line is marked `>>`) and the comment. " +
			"Line numbers reflect the code at review time – if the code has moved since, locate the spot by the snippet.\n\n",
		branch:    "- Branch: %s\n- Comparison: %s → %s\n",
		count:     "- Comments: %d\n\n",
		sideRight: "new version",
		sideLeft:  "original version – code removed or changed in the branch",
	},
	"cs": {
		title: "# Code review – připomínky\n\n",
		intro: "Zapracuj prosím následující připomínky z code review. " +
			"Každá obsahuje soubor, číslo řádku, úryvek kódu (řádek s připomínkou je označen `>>`) a komentář. " +
			"Čísla řádků odpovídají stavu v době review – pokud se kód mezitím posunul, najdi místo podle úryvku.\n\n",
		branch:    "- Větev: %s\n- Porovnání: %s → %s\n",
		count:     "- Počet připomínek: %d\n\n",
		sideRight: "nová verze",
		sideLeft:  "původní verze – kód odstraněný nebo změněný ve větvi",
	},
}

// codeSpan renders s as inline Markdown code, choosing a backtick run longer
// than any inside s (file and branch names may legally contain backticks).
func codeSpan(s string) string {
	ticks := "`"
	for strings.Contains(s, ticks) {
		ticks += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return ticks + s + ticks
}

// fenceFor returns a code fence longer than any backtick run in lines.
func fenceFor(lines []string) string {
	fence := "```"
	for _, l := range lines {
		for strings.Contains(l, fence) {
			fence += "`"
		}
	}
	return fence
}

// Markdown renders comments as a review document for Claude Code.
func Markdown(cs []Comment, info ExportInfo) string {
	tx, ok := exportTexts[info.Lang]
	if !ok {
		tx = exportTexts["en"]
	}
	var b strings.Builder
	b.WriteString(tx.title)
	b.WriteString(tx.intro)
	if info.Branch != "" || info.LeftDesc != "" {
		fmt.Fprintf(&b, tx.branch, codeSpan(info.Branch), codeSpan(info.LeftDesc), codeSpan(info.RightDesc))
	}
	fmt.Fprintf(&b, tx.count, len(cs))
	for i, c := range cs {
		side := tx.sideRight
		if c.Side == "left" {
			side = tx.sideLeft
		}
		fmt.Fprintf(&b, "## %d. %s (%s)\n\n", i+1, codeSpan(fmt.Sprintf("%s:%d", c.File, c.Line)), side)
		ctx, start := c.Context, c.ContextStart
		if len(ctx) == 0 {
			ctx, start = []string{c.LineText}, c.Line
		}
		fence := fenceFor(ctx)
		b.WriteString(fence + lang.ForPath(c.File) + "\n")
		for j, l := range ctx {
			mark := "  "
			if start+j == c.Line {
				mark = ">>"
			}
			fmt.Fprintf(&b, "%s %4d | %s\n", mark, start+j, l)
		}
		b.WriteString(fence + "\n\n")
		for l := range strings.SplitSeq(c.Text, "\n") {
			b.WriteString("> ")
			b.WriteString(l)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ExportFile is the name of the review file written to the repository root.
const ExportFile = "REVIEW.md"

// WriteExport writes REVIEW.md to root and makes sure git ignores it via
// the repository-local exclude file (not .gitignore, so nothing is committed).
func WriteExport(root, excludeFile, markdown string) (string, error) {
	target := filepath.Join(root, ExportFile)
	if st, err := os.Lstat(target); err == nil && !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s exists and is not a regular file", target)
	}
	if err := writeAtomic(target, []byte(markdown)); err != nil {
		return "", err
	}
	if err := ensureExcluded(excludeFile, "/"+ExportFile); err != nil {
		return target, fmt.Errorf("REVIEW.md written, but %s could not be updated: %w", excludeFile, err)
	}
	return target, nil
}

func ensureExcluded(excludeFile, pattern string) error {
	data, err := os.ReadFile(excludeFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for l := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(l) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(excludeFile), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(excludeFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	_, err = fmt.Fprintf(f, "%s# gitreview export\n%s\n", prefix, pattern)
	return err
}
