//go:build unix

package transcript

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// lockFile takes the advisory lock that marks f's one writer. It reports
// false when another open of the file holds it. The lock goes when f is
// closed or the process ends.
//
// It does not wait for a writer, but it does wait out a look: InUse holds
// a shared lock for a moment, which refuses this one as a writer's would,
// so a refusal counts only once it has lasted lockLook.
func lockFile(f *os.File) (bool, error) {
	var since time.Time
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			if since.IsZero() {
				since = time.Now()
			}
			if time.Since(since) >= lockLook {
				return false, nil
			}
			time.Sleep(time.Millisecond)
		default:
			return false, err
		}
	}
}

// lockLook is how long a refused lock is tried again before it counts:
// far longer than InUse holds its look, far shorter than a person notices.
const lockLook = 20 * time.Millisecond

// Hold takes the writer's lock on a file that is not a session's (a crash
// file), so that InUse reports it in use for as long as f is open. It
// reports false when another open of the file holds it.
func Hold(f *os.File) bool {
	ok, err := lockFile(f)
	return ok && err == nil
}

// InUse reports whether a gnosh holds the session file at path open for
// writing now: a session, a run or a sub-agent. It takes and lets go of a
// shared lock, which the writer's lock refuses; it reports false when the
// file cannot be read.
func InUse(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		switch {
		case err == nil:
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			return false
		case errors.Is(err, syscall.EINTR):
			continue
		default:
			return errors.Is(err, syscall.EWOULDBLOCK)
		}
	}
}
