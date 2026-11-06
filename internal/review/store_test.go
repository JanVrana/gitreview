package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreCRUD(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "gitreview"))
	if cs, err := s.List(); err != nil || len(cs) != 0 {
		t.Fatalf("empty list: %v %v", cs, err)
	}
	if _, err := s.Add(Comment{File: "a.go", Side: "right", Line: 1, Text: "  "}); err == nil {
		t.Fatal("empty text must fail")
	}
	if _, err := s.Add(Comment{File: "a.go", Side: "middle", Line: 1, Text: "x"}); err == nil {
		t.Fatal("bad side must fail")
	}
	b, err := s.Add(Comment{File: "b.go", Side: "right", Line: 5, Text: "druhý"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Add(Comment{File: "a.go", Side: "left", Line: 9, Text: "první"})
	cs, _ := s.List()
	if len(cs) != 2 || cs[0].ID != a.ID {
		t.Fatalf("sorting: %+v", cs)
	}
	if _, err := s.Update(b.ID, "upraveno"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	cs, _ = s.List()
	if len(cs) != 1 || cs[0].Text != "upraveno" {
		t.Fatalf("after update/delete: %+v", cs)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if cs, _ = s.List(); len(cs) != 0 {
		t.Fatal("clear failed")
	}
}

func TestMarkdownAndExport(t *testing.T) {
	cs := []Comment{{
		File: "lib/x.pl", Side: "right", Line: 11, LineText: "my $a = 1;",
		Context: []string{"sub x {", "my $a = 1;", "```"}, ContextStart: 10,
		Text: "Přejmenuj proměnnou\nna něco popisného",
	}}
	md := Markdown(cs, ExportInfo{Branch: "feature/x", LeftDesc: "develop", RightDesc: "worktree"})
	if !strings.Contains(md, "Please address") {
		t.Errorf("default export should be English:\n%s", md)
	}
	if cs := Markdown(cs, ExportInfo{Lang: "cs"}); !strings.Contains(cs, "Zapracuj") || !strings.Contains(cs, "nová verze") {
		t.Errorf("czech export:\n%s", cs)
	}
	for _, want := range []string{"`lib/x.pl:11`", ">>   11 | my $a = 1;", "````perl", "> na něco popisného", "feature/x"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}

	root := t.TempDir()
	exclude := filepath.Join(root, ".git", "info", "exclude")
	os.MkdirAll(filepath.Dir(exclude), 0o755)
	os.WriteFile(exclude, []byte("*.log"), 0o644)
	for range 2 {
		if _, err := WriteExport(root, exclude, md); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(filepath.Join(root, ExportFile))
	if string(got) != md {
		t.Fatal("REVIEW.md content differs")
	}
	ex, _ := os.ReadFile(exclude)
	if strings.Count(string(ex), "/REVIEW.md") != 1 || !strings.HasPrefix(string(ex), "*.log\n") {
		t.Fatalf("exclude file: %q", ex)
	}
}

func TestMarkdownBackticksInNames(t *testing.T) {
	md := Markdown([]Comment{{File: "we`ird.go", Side: "right", Line: 1, LineText: "x", Text: "t"}},
		ExportInfo{Branch: "feat`x", LeftDesc: "a", RightDesc: "b"})
	if !strings.Contains(md, "``we`ird.go:1``") || !strings.Contains(md, "``feat`x``") {
		t.Fatalf("backticks not escaped:\n%s", md)
	}
}
