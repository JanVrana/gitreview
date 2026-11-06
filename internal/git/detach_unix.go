//go:build unix

package git

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a new session without a controlling terminal, so
// credential prompts (ssh passphrase, host key confirmation) fail instead of
// grabbing the terminal gitreview runs in.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
