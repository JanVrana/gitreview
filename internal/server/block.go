package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"

	"github.com/JanVrana/gitreview/internal/diff"
	"github.com/JanVrana/gitreview/internal/git"
)

func contentHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// blockUndo remembers the last block revert so it can be undone.
type blockUndo struct {
	path   string
	before []byte
	after  string // hash of the file right after the revert
}

var errFileChanged = httpError{http.StatusConflict, errors.New("the file changed meanwhile; check the diff again")}

// revertBlock replaces one changed block of the working-tree file with the
// left side's lines (like IntelliJ's »). The comparison comes from the query
// (as for /api/diff); the client sends the block and the file hash it saw.
func (s *Server) revertBlock(r *http.Request) (any, error) {
	var body struct {
		Path  string     `json:"path"`
		Hash  string     `json:"hash"`
		Block diff.Block `json:"block"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	unlock, err := s.lockWrite()
	if err != nil {
		return nil, err
	}
	defer unlock()
	fd, err := s.loadDiff(r, body.Path, true)
	if err != nil {
		return nil, err
	}
	if !fd.blockRevertible() {
		return nil, badRequest(errors.New("blocks of this file cannot be reverted"))
	}
	if contentHash(fd.rightData) != body.Hash {
		return nil, errFileChanged
	}
	b := body.Block
	b.Type = "change"
	if !slices.Contains(fd.blocks, b) {
		return nil, errFileChanged
	}
	out, err := git.ReplaceLines(fd.rightData, b.RightStart, b.RightLen, fd.leftData, b.LeftStart, b.LeftLen)
	if err != nil {
		return nil, err
	}
	if err := s.repo.WriteWorktree(body.Path, out); err != nil {
		return nil, err
	}
	s.lastRevert = &blockUndo{path: body.Path, before: fd.rightData, after: contentHash(out)}
	return map[string]any{"ok": true}, nil
}

// undoRevertBlock restores the file as it was before the last block revert,
// provided nobody changed it since.
func (s *Server) undoRevertBlock(r *http.Request) (any, error) {
	if err := decode(r, &struct{}{}); err != nil {
		return nil, err
	}
	unlock, err := s.lockWrite()
	if err != nil {
		return nil, err
	}
	defer unlock()
	u := s.lastRevert
	if u == nil {
		return nil, httpError{http.StatusConflict, errors.New("nothing to undo")}
	}
	cur, err := s.repo.Content("", u.path)
	if err != nil || contentHash(cur) != u.after {
		return nil, errFileChanged
	}
	if err := s.repo.WriteWorktree(u.path, u.before); err != nil {
		return nil, err
	}
	s.lastRevert = nil
	return map[string]any{"path": u.path}, nil
}
