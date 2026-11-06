package git

import "testing"

func TestReplaceLines(t *testing.T) {
	for _, tc := range []struct {
		name, dst string
		ds, dl    int
		src       string
		ss, sl    int
		want      string
	}{
		{"modify", "a\nX\nc\n", 1, 1, "a\nb\nc\n", 1, 1, "a\nb\nc\n"},
		{"drop insertion", "a\nX\nY\nb\n", 1, 2, "a\nb\n", 1, 0, "a\nb\n"},
		{"restore deletion", "a\nc\n", 1, 0, "a\nb\nc\n", 1, 1, "a\nb\nc\n"},
		{"crlf file", "a\r\nX\r\nc\r\n", 1, 1, "a\nb\nc\n", 1, 1, "a\r\nb\r\nc\r\n"},
		{"restore missing final newline", "a\nb\n", 1, 1, "a\nb", 1, 1, "a\nb"},
		{"add final newline back", "a\nb", 1, 1, "a\nb\n", 1, 1, "a\nb\n"},
		{"source line without newline in the middle", "a\nX\nc\n", 1, 1, "a\nb", 1, 1, "a\nb\nc\n"},
		{"restore at end", "a\n", 1, 0, "a\nb\nc\n", 1, 2, "a\nb\nc\n"},
		{"drop at end", "a\nb\nc", 1, 2, "a\n", 1, 0, "a\n"},
		{"empty dst", "", 0, 0, "a\n", 0, 1, "a\n"},
	} {
		got, err := ReplaceLines([]byte(tc.dst), tc.ds, tc.dl, []byte(tc.src), tc.ss, tc.sl)
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	if _, err := ReplaceLines([]byte("a\n"), 1, 1, []byte("a\n"), 0, 1); err == nil {
		t.Error("out of range accepted")
	}
}
