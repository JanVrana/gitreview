// Package update replaces the running gitreview binary with the latest
// GitHub release. Downloads are verified against the release's SHA256SUMS.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// maxBinary caps the download size (release binaries are ~7 MB).
const maxBinary = 100 << 20

// Updater checks and installs releases of one GitHub repository.
type Updater struct {
	Repo    string // "owner/name"
	BaseURL string // "https://github.com"; overridden in tests
	Client  *http.Client
}

// New returns an Updater for the gitreview releases on GitHub.
func New() *Updater {
	return &Updater{Repo: "JanVrana/gitreview", BaseURL: "https://github.com", Client: &http.Client{Timeout: 2 * time.Minute}}
}

var semverRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

func parse(v string) ([3]int, bool) {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var r [3]int
	for i := range r {
		r[i], _ = strconv.Atoi(m[i+1])
	}
	return r, true
}

// Release reports whether v is a plain release version (vX.Y.Z). Development
// builds ("dev", "v0.3.0-2-gabc-dirty") are never updated automatically.
func Release(v string) bool {
	_, ok := parse(v)
	return ok
}

// Newer reports whether release version a is newer than b.
func Newer(a, b string) bool {
	x, ok1 := parse(a)
	y, ok2 := parse(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// Latest returns the tag of the latest release. It follows GitHub's
// /releases/latest redirect instead of the API, which is rate limited.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.BaseURL+"/"+u.Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	client := *u.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode/100 != 3 || !strings.Contains(loc, "/releases/tag/") {
		return "", fmt.Errorf("latest release: unexpected response %s (Location %q)", res.Status, loc)
	}
	tag := path.Base(loc)
	if !Release(tag) {
		return "", fmt.Errorf("latest release: unexpected tag %q", tag)
	}
	return tag, nil
}

// AssetName is the release file for this platform.
func AssetName() string {
	return "gitreview-" + runtime.GOOS + "-" + runtime.GOARCH
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: response too large", url)
	}
	return data, nil
}

// Install downloads release tag for this platform, verifies its checksum
// and atomically replaces the file at exe (the running binary).
func (u *Updater) Install(ctx context.Context, tag, exe string) error {
	if !Release(tag) {
		return fmt.Errorf("invalid release tag %q", tag)
	}
	base := u.BaseURL + "/" + u.Repo + "/releases/download/" + tag + "/"
	asset := AssetName()
	sums, err := u.get(ctx, base+"SHA256SUMS", 64<<10)
	if err != nil {
		return err
	}
	want, err := checksum(sums, asset)
	if err != nil {
		return err
	}
	bin, err := u.get(ctx, base+asset, maxBinary)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(bin); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s %s: checksum mismatch", asset, tag)
	}
	return replace(exe, bin)
}

func checksum(sums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s is not listed in SHA256SUMS", asset)
}

// replace writes data next to exe and renames it over exe, so the binary is
// never half-written. The running process keeps its (now unlinked) inode.
func replace(exe string, data []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".gitreview-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), exe)
}

// Executable returns the resolved path of the running binary.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// ErrUpToDate is returned by Check when no newer release exists.
var ErrUpToDate = errors.New("already up to date")

// Check installs the latest release over exe when it is newer than current.
// It returns the installed tag, or ErrUpToDate.
func (u *Updater) Check(ctx context.Context, current, exe string) (string, error) {
	tag, err := u.Latest(ctx)
	if err != nil {
		return "", err
	}
	if !Newer(tag, current) {
		return tag, ErrUpToDate
	}
	if err := u.Install(ctx, tag, exe); err != nil {
		return "", err
	}
	return tag, nil
}
