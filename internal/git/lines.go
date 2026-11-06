package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// splitKeep splits b into lines that keep their terminators.
func splitKeep(b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, b)
			break
		}
		out = append(out, b[:i+1])
		b = b[i+1:]
	}
	return out
}

// ReplaceLines replaces dst lines [ds, ds+dl) with src lines [ss, ss+sl)
// (0-based, counted like SplitLines). Inserted lines get dst's line ending;
// at the end of dst the last one ends like it does in src, so a missing
// final newline is restored too.
func ReplaceLines(dst []byte, ds, dl int, src []byte, ss, sl int) ([]byte, error) {
	d, s := splitKeep(dst), splitKeep(src)
	if ds < 0 || dl < 0 || ds+dl > len(d) || ss < 0 || sl < 0 || ss+sl > len(s) {
		return nil, errors.New("line range out of bounds")
	}
	eol := []byte("\n")
	if len(d) > 0 && bytes.HasSuffix(d[0], []byte("\r\n")) {
		eol = []byte("\r\n")
	}
	atEnd := ds+dl == len(d)
	var out bytes.Buffer
	for _, l := range d[:ds] {
		out.Write(l)
	}
	if ds > 0 && !bytes.HasSuffix(d[ds-1], []byte("\n")) && sl > 0 {
		out.Write(eol) // cannot happen for git's line counting, but stay safe
	}
	for i, l := range s[ss : ss+sl] {
		body := bytes.TrimSuffix(bytes.TrimSuffix(l, []byte("\n")), []byte("\r"))
		out.Write(body)
		last := i == sl-1
		if !last || !atEnd || bytes.HasSuffix(l, []byte("\n")) {
			out.Write(eol)
		}
	}
	for _, l := range d[ds+dl:] {
		out.Write(l)
	}
	return out.Bytes(), nil
}

// WriteWorktree replaces the content of an existing regular file in the
// working tree, keeping its permissions. The new content is written to a
// temporary file first, so a failure never leaves the file half written.
func (r *Repo) WriteWorktree(p string, data []byte) error {
	full, err := r.worktreePath(p)
	if err != nil {
		return err
	}
	st, err := os.Lstat(full)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", p)
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".gitreview-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), st.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), full)
}
