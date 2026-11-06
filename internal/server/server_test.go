package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/JanVrana/gitreview/internal/git"
	"github.com/JanVrana/gitreview/internal/testrepo"
)

func newTest(t *testing.T) (*httptest.Server, string) {
	return newTestOpts(t, Options{AllowHosts: []string{"myvm"}})
}

func newTestOpts(t *testing.T, opts Options) (*httptest.Server, string) {
	dir := testrepo.New(t)
	r, err := git.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(r, fstest.MapFS{"index.html": {Data: []byte("ok")}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, dir
}

func get(t *testing.T, ts *httptest.Server, path string, v any) int {
	t.Helper()
	res, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if v != nil {
		json.NewDecoder(res.Body).Decode(v)
	}
	return res.StatusCode
}

func TestFilesAndDiff(t *testing.T) {
	ts, _ := newTest(t)
	for _, q := range []string{"base=develop", "base=master&left=tip", "base=develop&right=head", "base=develop&ws=1"} {
		var out struct {
			Files []git.FileChange
		}
		if code := get(t, ts, "/api/files?"+q, &out); code != 200 || len(out.Files) == 0 {
			t.Fatalf("%s: %d %+v", q, code, out)
		}
	}
	var d struct {
		Blocks []map[string]any
		Left   struct {
			Lines  []string
			Exists bool
		}
		Right struct {
			Lines  []string
			Exists bool
		}
		Binary bool
	}
	if code := get(t, ts, "/api/diff?base=develop&path=main.go", &d); code != 200 || len(d.Blocks) < 3 || !d.Left.Exists {
		t.Fatalf("diff main.go: %d %+v", code, d)
	}
	d.Blocks = nil
	get(t, ts, "/api/diff?base=develop&path="+url.QueryEscape("nový soubor.txt"), &d)
	if len(d.Blocks) != 1 || len(d.Right.Lines) != 2 {
		t.Fatalf("untracked: %+v", d)
	}
	d.Binary = false
	get(t, ts, "/api/diff?base=develop&path=bin.dat", &d)
	if !d.Binary {
		t.Fatal("binary not detected")
	}
}

func TestRejectsBadInput(t *testing.T) {
	ts, _ := newTest(t)
	for _, q := range []string{
		"/api/diff?base=develop&path=../../etc/passwd",
		"/api/diff?base=develop&path=" + url.QueryEscape("/etc/passwd"),
		"/api/diff?base=develop&path=.git/config",
		"/api/diff?base=develop&path=renamed.txt%2F..%2F..%2Fx",
	} {
		if code := get(t, ts, q, nil); code != http.StatusNotFound {
			t.Errorf("%s: got %d", q, code)
		}
	}
	for _, b := range []string{"--output=x", "nope", "develop..master"} {
		if code := get(t, ts, "/api/files?base="+url.QueryEscape(b), nil); code != http.StatusBadRequest {
			t.Errorf("base %q: got %d", b, code)
		}
	}
}

func TestCommentsAndExport(t *testing.T) {
	ts, dir := newTest(t)
	body := `{"file":"main.go","side":"right","line":5,"lineText":"main CHANGED line","context":["a","main CHANGED line"],"contextStart":4,"text":"Proč?"}`
	res, err := http.Post(ts.URL+"/api/comments", "application/json", strings.NewReader(body))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("add: %v %v", err, res.Status)
	}
	res, _ = http.Post(ts.URL+"/api/export", "application/json", strings.NewReader(`{"write":true,"left":"develop","right":"wt"}`))
	var ex struct {
		Markdown, Path string
		Count          int
	}
	json.NewDecoder(res.Body).Decode(&ex)
	if ex.Count != 1 || !strings.Contains(ex.Markdown, "main.go:5") || !strings.HasSuffix(ex.Path, "REVIEW.md") {
		t.Fatalf("export: %+v", ex)
	}
	// REVIEW.md must be ignored by git and hidden from the list
	if st := testrepo.Git(t, dir, "status", "--porcelain"); strings.Contains(st, "REVIEW.md") {
		t.Fatalf("REVIEW.md not excluded:\n%s", st)
	}
}

func do(t *testing.T, method, u, host, ctype, origin, body string) int {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	if host != "" {
		req.Host = host
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestCrossSiteProtection(t *testing.T) {
	ts, _ := newTest(t)
	u, _ := url.Parse(ts.URL)
	body := `{"file":"main.go","side":"right","line":1,"text":"x"}`
	cases := []struct {
		name, method, host, ctype, origin string
		want                              int
	}{
		{"simple text/plain POST", "POST", "", "text/plain", "", http.StatusUnsupportedMediaType},
		{"form POST", "POST", "", "application/x-www-form-urlencoded", "", http.StatusUnsupportedMediaType},
		{"foreign origin", "POST", "", "application/json", "http://evil.example", http.StatusForbidden},
		{"rebinding host", "GET", "evil.example:" + u.Port(), "", "", http.StatusForbidden},
		{"same origin", "POST", "", "application/json", ts.URL, http.StatusOK},
		{"allowed hostname", "GET", "myvm:" + u.Port(), "", "", http.StatusOK},
		{"localhost", "GET", "localhost:" + u.Port(), "", "", http.StatusOK},
	}
	for _, c := range cases {
		path := "/api/comments"
		if got := do(t, c.method, ts.URL+path, c.host, c.ctype, c.origin, body); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestEmptyFileDiff(t *testing.T) {
	ts, dir := newTest(t)
	testrepo.Write(t, dir, "empty.txt", "")
	var d struct{ Blocks []map[string]any }
	if code := get(t, ts, "/api/diff?base=develop&path=empty.txt", &d); code != 200 || d.Blocks == nil {
		t.Fatalf("empty file: %d %+v", code, d)
	}
}

func TestEventsOnChange(t *testing.T) {
	ts, dir := newTest(t)
	res, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "event: ") {
				got <- strings.TrimPrefix(sc.Text(), "event: ")
			}
		}
	}()
	if ev := <-got; ev != "hello" {
		t.Fatalf("first event %q", ev)
	}
	testrepo.Write(t, dir, "main.go", "changed by Claude\n")
	select {
	case ev := <-got:
		if ev != "changed" {
			t.Fatalf("event %q", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no change event")
	}
}

func TestTokenAuth(t *testing.T) {
	const token = "s3cret-token"
	ts, _ := newTestOpts(t, Options{Token: token})
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	status := func(req *http.Request) (*http.Response, int) {
		t.Helper()
		res, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res, res.StatusCode
	}
	req := func(method, path string) *http.Request {
		r, _ := http.NewRequest(method, ts.URL+path, nil)
		return r
	}

	for _, p := range []string{"/", "/api/info", "/api/events", "/?token=wrong", "/api/info?token=wrong"} {
		if _, code := status(req("GET", p)); code != http.StatusUnauthorized {
			t.Errorf("GET %s without valid token: %d, want 401", p, code)
		}
	}

	// page load with the token: cookie + redirect to the clean URL
	res, code := status(req("GET", "/?token="+token+"&x=1"))
	if code != http.StatusSeeOther || res.Header.Get("Location") != "/?x=1" {
		t.Fatalf("token page load: %d %q", code, res.Header.Get("Location"))
	}
	cookies := res.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Value != token {
		t.Fatalf("cookie: %+v", cookies)
	}

	withCookie := req("GET", "/api/info")
	withCookie.AddCookie(cookies[0])
	if _, code := status(withCookie); code != http.StatusOK {
		t.Errorf("with cookie: %d", code)
	}
	bad := req("GET", "/api/info")
	bad.AddCookie(&http.Cookie{Name: cookies[0].Name, Value: "nope"})
	if _, code := status(bad); code != http.StatusUnauthorized {
		t.Errorf("bad cookie: %d", code)
	}
	bearer := req("GET", "/api/info")
	bearer.Header.Set("Authorization", "Bearer "+token)
	if _, code := status(bearer); code != http.StatusOK {
		t.Errorf("bearer: %d", code)
	}
	// API call with the token in the query is served directly (no redirect)
	if _, code := status(req("GET", "/api/info?token="+token)); code != http.StatusOK {
		t.Errorf("api with token query: %d", code)
	}
}
