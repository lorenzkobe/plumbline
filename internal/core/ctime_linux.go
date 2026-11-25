package core

import (
	"os"
	"syscall"
	"time"
)

// changeTime: when the file's inode last changed (a write, rename, link,
// chmod or utimes), which a copy that keeps the old modified time can't set back.
func changeTime(fi os.FileInfo) (time.Time, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(st.Ctim.Sec, st.Ctim.Nsec), true
}
