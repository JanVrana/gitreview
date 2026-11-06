//go:build !unix

package git

import "os/exec"

func detach(*exec.Cmd) {}
