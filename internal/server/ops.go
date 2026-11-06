package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/JanVrana/gitreview/internal/git"
)

// ---- write operations ------------------------------------------------------------
// All git writes are serialized by s.writing; a second one gets 409 instead of
// queueing behind a slow network operation.

func (s *Server) lockWrite() (func(), error) {
	if !s.writing.TryLock() {
		return nil, httpError{http.StatusConflict, errors.New("another git operation is running")}
	}
	return s.writing.Unlock, nil
}

// gitError reports git failures (rejected push, conflicting pull, …) as 409:
// the request was fine, the repository state does not allow it.
func gitError(err error) error {
	if err == nil {
		return nil
	}
	var he httpError
	if errors.As(err, &he) {
		return err
	}
	return httpError{http.StatusConflict, err}
}

type pendingFile struct {
	Path      string `json:"path"`
	Untracked bool   `json:"untracked"`
}

// status lists the uncommitted files (what the commit panel offers).
func (s *Server) status(*http.Request) (any, error) {
	pending, err := s.repo.Uncommitted()
	if err != nil {
		return nil, err
	}
	files := make([]pendingFile, 0, len(pending))
	for _, p := range pending {
		files = append(files, pendingFile{p.Path, p.Untracked})
	}
	slices.SortFunc(files, func(a, b pendingFile) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})
	return map[string]any{"head": s.repo.Head().Sha, "files": files}, nil
}

func (s *Server) commit(r *http.Request) (any, error) {
	var body struct {
		Paths   []string `json:"paths"`
		Message string   `json:"message"`
		Head    string   `json:"head"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	unlock, err := s.lockWrite()
	if err != nil {
		return nil, err
	}
	defer unlock()
	sha, err := s.repo.Commit(body.Paths, body.Message, body.Head)
	if err != nil {
		return nil, gitError(err)
	}
	return map[string]any{"sha": sha}, nil
}

func (s *Server) revert(r *http.Request) (any, error) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	unlock, err := s.lockWrite()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.repo.Revert(body.Path); err != nil {
		return nil, gitError(err)
	}
	return map[string]any{"ok": true}, nil
}

func (s *Server) push(r *http.Request) (any, error) {
	if err := decode(r, &struct{}{}); err != nil {
		return nil, err
	}
	unlock, err := s.lockWrite()
	if err != nil {
		return nil, err
	}
	defer unlock()
	res, err := s.repo.Push()
	if err != nil {
		return nil, gitError(err)
	}
	return res, nil
}

func (s *Server) pull(r *http.Request) (any, error) {
	if err := decode(r, &struct{}{}); err != nil {
		return nil, err
	}
	unlock, err := s.lockWrite()
	if err != nil {
		return nil, err
	}
	defer unlock()
	res, err := s.repo.Pull()
	if err != nil {
		return nil, gitError(err)
	}
	return res, nil
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

func (s *Server) stashAction(r *http.Request) (any, error) {
	var body struct {
		Sha    string `json:"sha"`
		Action string `json:"action"` // apply | pop | drop
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if !shaRe.MatchString(body.Sha) {
		return nil, badRequest(errors.New("invalid stash"))
	}
	unlock, err := s.lockWrite()
	if err != nil {
		return nil, err
	}
	defer unlock()
	switch body.Action {
	case "apply", "pop":
		err = s.repo.StashApply(body.Sha, body.Action == "pop")
	case "drop":
		err = s.repo.StashDrop(body.Sha)
	default:
		return nil, badRequest(errors.New("unknown stash action"))
	}
	if err != nil {
		return nil, gitError(err)
	}
	return map[string]any{"ok": true}, nil
}

// ---- updates ---------------------------------------------------------------------

// Updater checks and installs gitreview releases (implemented in main).
type Updater interface {
	// Latest returns the newest release and whether it is newer than the
	// running version.
	Latest(ctx context.Context) (tag string, newer bool, err error)
	// Install replaces the binary with the latest release and returns its tag.
	Install(ctx context.Context) (tag string, err error)
	// Restart replaces the running process with the installed binary; it
	// returns only on failure.
	Restart() error
	// Auto reports whether automatic updates are enabled.
	Auto() bool
}

func (s *Server) noUpdater() error {
	return httpError{http.StatusNotImplemented, errors.New("updates are not supported on this platform")}
}

func (s *Server) updateCheck(r *http.Request) (any, error) {
	if s.updater == nil {
		return nil, s.noUpdater()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	tag, newer, err := s.updater.Latest(ctx)
	if err != nil {
		return nil, httpError{http.StatusBadGateway, err}
	}
	return map[string]any{"current": s.version, "latest": tag, "newer": newer, "auto": s.updater.Auto()}, nil
}

// updateNow installs the latest release, answers, and then restarts into it.
// Open pages reload when they see the new version after reconnecting.
func (s *Server) updateNow(r *http.Request) (any, error) {
	if err := decode(r, &struct{}{}); err != nil {
		return nil, err
	}
	if s.updater == nil {
		return nil, s.noUpdater()
	}
	if !s.updating.TryLock() {
		return nil, httpError{http.StatusConflict, errors.New("an update is already running")}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	tag, err := s.updater.Install(ctx)
	if err != nil {
		s.updating.Unlock()
		return nil, httpError{http.StatusBadGateway, err}
	}
	go func() {
		time.Sleep(500 * time.Millisecond) // let the response reach the browser
		s.PrepareRestart()
		err := s.updater.Restart()
		s.AbortRestart()
		s.updating.Unlock()
		log.Printf("update: restart failed, restart gitreview manually to use %s: %v", tag, err)
	}()
	return map[string]any{"installed": tag}, nil
}

// stashes is the stash list shown in the base selector.
func (s *Server) stashes() []git.Stash {
	list, err := s.repo.Stashes()
	if err != nil {
		return []git.Stash{}
	}
	return list
}
