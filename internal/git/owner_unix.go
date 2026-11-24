//go:build unix

package git

import (
	"os"
	"syscall"
)

func owner(st os.FileInfo) int {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(s.Uid)
	}
	return -1
}
