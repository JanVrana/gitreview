package server

import (
	"errors"
	"net/http"

	"github.com/JanVrana/gitreview/internal/git"
)

// blame annotates both sides of a changed file: who changed each line last.
func (s *Server) blame(r *http.Request) (any, error) {
	fd, err := s.loadDiff(r, r.URL.Query().Get("path"), true)
	if err != nil {
		return nil, err
	}
	if fd.binary {
		return nil, badRequest(errors.New("binary file"))
	}
	resp := map[string]*git.Blame{}
	if fd.left.Exists {
		oldPath := fd.fc.Path
		if fd.fc.OldPath != "" {
			oldPath = fd.fc.OldPath
		}
		if resp["left"], err = s.repo.Blame(fd.c.Left.Rev, oldPath, fd.c.IgnoreW); err != nil {
			return nil, err
		}
	}
	if fd.right.Exists {
		rev := fd.c.Right.Rev
		if fd.fc.Src != "" {
			rev = fd.fc.Src
		}
		b, err := s.repo.Blame(rev, fd.fc.Path, fd.c.IgnoreW)
		if err != nil && rev == "" {
			// a new file git does not know yet: nothing of it is committed
			b, err = git.NotCommitted(len(fd.right.Lines)), nil
		}
		if err != nil {
			return nil, err
		}
		resp["right"] = b
	}
	return resp, nil
}
