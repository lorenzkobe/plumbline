package core

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockWait is how long a hook waits for another hook of the same task.
var lockWait = 10 * time.Second

// LockTask serializes changes to one task's state. Hooks of the lead and of a
// background helper run at the same time; without the lock, one hook's save
// can wipe out another's (a helper's finish, a refused file). Returns the
// unlock function. If the lock can't be taken in time, it goes on without it
// rather than hang the session.
func LockTask(root, id string) func() {
	p := filepath.Join(TaskDir(root, id), "state.lock")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	deadline := time.Now().Add(lockWait)
	for {
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}
		}
		if time.Now().After(deadline) {
			f.Close()
			return func() {}
		}
		time.Sleep(15 * time.Millisecond)
	}
}
