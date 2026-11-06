package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JanVrana/gitreview/internal/git"
	"github.com/JanVrana/gitreview/internal/testrepo"
)

func post(t *testing.T, ts *httptest.Server, path, body string, v any) int {
	t.Helper()
	res, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if v != nil {
		json.NewDecoder(res.Body).Decode(v)
	}
	return res.StatusCode
}

func identity(t *testing.T) {
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t", "GIT_CONFIG_GLOBAL": "/dev/null"} {
		t.Setenv(k, v)
	}
}

func TestCommitAndRevertEndpoints(t *testing.T) {
	identity(t)
	ts, dir := newTest(t)
	var st struct {
		Head  string
		Files []pendingFile
	}
	if get(t, ts, "/api/status", &st); len(st.Files) != 3 || st.Head == "" {
		t.Fatalf("status: %+v", st)
	}
	var files struct{ Files []git.FileChange }
	get(t, ts, "/api/files?base=develop", &files)
	for _, f := range files.Files {
		if want := f.Path != "renamed.txt" && f.Path != "delete-me.txt"; f.Uncommitted != want {
			t.Errorf("%s: uncommitted %v", f.Path, f.Uncommitted)
		}
	}

	if code := post(t, ts, "/api/commit", `{"paths":["main.go"],"message":"m","head":"0000000000000000000000000000000000000000"}`, nil); code != http.StatusConflict {
		t.Fatalf("stale head: %d, want 409", code)
	}
	var c struct{ Sha string }
	if code := post(t, ts, "/api/commit", `{"paths":["main.go"],"message":"m","head":"`+st.Head+`"}`, &c); code != 200 || c.Sha == st.Head {
		t.Fatalf("commit: %d %+v", code, c)
	}
	if code := post(t, ts, "/api/revert", `{"path":"bin.dat"}`, nil); code != 200 {
		t.Fatalf("revert: %d", code)
	}
	if code := post(t, ts, "/api/revert", `{"path":"bin.dat"}`, nil); code != http.StatusConflict {
		t.Fatalf("second revert: %d, want 409", code)
	}
	if s := testrepo.Git(t, dir, "status", "--porcelain"); strings.TrimSpace(s) != `?? "nov\303\275 soubor.txt"` {
		t.Fatalf("status: %q", s)
	}
}

func TestWritesAreSerialized(t *testing.T) {
	ts, _ := newTest(t)
	srv := testServers[ts]
	srv.writing.Lock()
	defer srv.writing.Unlock()
	if code := post(t, ts, "/api/revert", `{"path":"main.go"}`, nil); code != http.StatusConflict {
		t.Fatalf("concurrent write: %d, want 409", code)
	}
}

func TestStashEndpoints(t *testing.T) {
	identity(t)
	ts, dir := newTest(t)
	testrepo.Git(t, dir, "stash", "push", "-q", "-u", "-m", "wip")
	var info struct{ Stashes []git.Stash }
	get(t, ts, "/api/info", &info)
	if len(info.Stashes) != 1 {
		t.Fatalf("stashes: %+v", info.Stashes)
	}
	sha := info.Stashes[0].Sha
	var files struct {
		Compare compare
		Files   []git.FileChange
	}
	if code := get(t, ts, "/api/files?stash="+sha, &files); code != 200 || len(files.Files) != 3 || files.Compare.Stash == nil {
		t.Fatalf("stash files: %d %+v", code, files)
	}
	var d struct {
		Right struct {
			Lines  []string
			Exists bool
		}
	}
	get(t, ts, "/api/diff?stash="+sha+"&path=nov%C3%BD%20soubor.txt", &d)
	if !d.Right.Exists || len(d.Right.Lines) != 2 {
		t.Fatalf("untracked stash file: %+v", d)
	}
	for _, bad := range []string{"HEAD", "--all", strings.Repeat("0", 40)} {
		if code := get(t, ts, "/api/files?stash="+bad, nil); code < 400 {
			t.Errorf("stash=%s accepted: %d", bad, code)
		}
	}
	if code := post(t, ts, "/api/stash", `{"sha":"`+sha+`","action":"pop"}`, nil); code != 200 {
		t.Fatalf("pop: %d", code)
	}
	if code := post(t, ts, "/api/stash", `{"sha":"`+sha+`","action":"drop"}`, nil); code != http.StatusConflict {
		t.Fatalf("drop after pop: %d, want 409", code)
	}
}

type fakeUpdater struct{ installed bool }

func (f *fakeUpdater) Latest(context.Context) (string, bool, error) { return "v9.9.9", true, nil }
func (f *fakeUpdater) Install(context.Context) (string, error) {
	f.installed = true
	return "v9.9.9", nil
}
func (f *fakeUpdater) Restart() error { return errors.New("no restart in tests") }
func (f *fakeUpdater) Auto() bool     { return false }

func TestUpdateEndpoints(t *testing.T) {
	ts, _ := newTest(t)
	if code := get(t, ts, "/api/update", nil); code != http.StatusNotImplemented {
		t.Fatalf("no updater: %d, want 501", code)
	}
	fu := &fakeUpdater{}
	ts, _ = newTestOpts(t, Options{Updater: fu, Version: "v0.5.0", Token: "secret"})
	if code := get(t, ts, "/api/update", nil); code != http.StatusUnauthorized {
		t.Fatalf("without token: %d, want 401", code)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/update", nil)
	req.Header.Set("Authorization", "Bearer secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Current, Latest string
		Newer           bool
	}
	json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	if out.Current != "v0.5.0" || out.Latest != "v9.9.9" || !out.Newer {
		t.Fatalf("check: %+v", out)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/update", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || !fu.installed {
		t.Fatalf("update now: %d installed=%v", res.StatusCode, fu.installed)
	}
}
