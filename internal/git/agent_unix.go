//go:build unix

package git

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// agentSocket returns the ssh-agent socket network commands should use.
//
// gitreview often runs much longer than the session it was started from: the
// SSH_AUTH_SOCK it inherited may be gone (a forwarded agent of an ended ssh
// login) or point to an agent without keys while another one holds them
// (e.g. a systemd ssh-agent next to the desktop keyring's). So the inherited
// socket is used only when its agent has keys; otherwise the usual agent
// sockets of this user are tried, and the first one with keys wins.
// "" = leave SSH_AUTH_SOCK as inherited.
func agentSocket() string {
	env := os.Getenv("SSH_AUTH_SOCK")
	if n, err := agentKeys(env); err == nil && n > 0 {
		return env
	}
	for _, s := range agentCandidates() {
		if s == env {
			continue
		}
		if n, err := agentKeys(s); err == nil && n > 0 {
			return s
		}
	}
	return ""
}

func agentCandidates() []string {
	uid := os.Getuid()
	run := os.Getenv("XDG_RUNTIME_DIR")
	if run == "" {
		run = fmt.Sprintf("/run/user/%d", uid)
	}
	res := []string{
		filepath.Join(run, "gcr/ssh"),
		filepath.Join(run, "keyring/ssh"),
		filepath.Join(run, "ssh-agent.socket"),
		filepath.Join(run, "openssh_agent"),
		filepath.Join(run, "gnupg/S.gpg-agent.ssh"),
	}
	// forwarded or ad hoc agents: /tmp/ssh-XXXX/agent.<pid>, newest first
	tmp, _ := filepath.Glob("/tmp/ssh-*/agent.*")
	type sock struct {
		path string
		mod  time.Time
	}
	var socks []sock
	for _, p := range tmp {
		if st, err := os.Stat(p); err == nil && st.Mode()&os.ModeSocket != 0 && owner(st) == uid {
			socks = append(socks, sock{p, st.ModTime()})
		}
	}
	sort.Slice(socks, func(i, j int) bool { return socks[i].mod.After(socks[j].mod) })
	for _, s := range socks {
		res = append(res, s.path)
	}
	return res
}

// agentKeys asks the agent at path how many identities it holds
// (SSH2_AGENTC_REQUEST_IDENTITIES).
func agentKeys(path string) (int, error) {
	if path == "" {
		return 0, os.ErrNotExist
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte{0, 0, 0, 1, 11}); err != nil {
		return 0, err
	}
	var hdr [9]byte // length, type 12 (IDENTITIES_ANSWER), count
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return 0, err
	}
	if hdr[4] != 12 {
		return 0, fmt.Errorf("unexpected agent reply %d", hdr[4])
	}
	return int(binary.BigEndian.Uint32(hdr[5:])), nil
}
