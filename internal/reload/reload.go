// Package reload lets the `pgkeeper -reload` command ask the running daemon
// to re-read its configuration: the daemon records its PID in a file under
// global.temp-path and reloads on SIGHUP.
package reload

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// PIDFile returns the PID file location for a given global.temp-path. The
// name starts with a dot so it can never clash with a backup directory.
func PIDFile(tempPath string) string {
	return filepath.Join(tempPath, "pgkeeper", ".pgkeeper.pid")
}

// WritePID records the current process as the running daemon.
func WritePID(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RemovePID deletes the PID file if it still belongs to this process.
func RemovePID(path string) {
	if pid, err := readPID(path); err == nil && pid == os.Getpid() {
		_ = os.Remove(path)
	}
}

func readPID(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid PID file %s", path)
	}
	return pid, nil
}

// Signal sends SIGHUP to the daemon recorded in the PID file and returns its
// PID. Stale PID files are detected (no such process, or a process that is
// not pgkeeper) so an unrelated process is never signalled.
func Signal(path string) (int, error) {
	pid, err := readPID(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("no running pgkeeper found (%s does not exist; is it started and using the same global.temp-path?)", path)
	}
	if err != nil {
		return 0, err
	}
	if err := syscall.Kill(pid, 0); err != nil {
		switch {
		case errors.Is(err, syscall.ESRCH):
			return pid, fmt.Errorf("pgkeeper is not running (stale PID file for pid %d)", pid)
		case errors.Is(err, syscall.EPERM):
			return pid, fmt.Errorf("not allowed to signal pid %d: run -reload as the user running pgkeeper", pid)
		default:
			return pid, err
		}
	}
	// Guard against PID reuse after a crash or reboot (Linux only).
	if cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil && !bytes.Contains(cmd, []byte("pgkeeper")) {
		return pid, fmt.Errorf("pid %d from %s is not a pgkeeper process (stale PID file)", pid, path)
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
		return pid, fmt.Errorf("signal pid %d: %w", pid, err)
	}
	return pid, nil
}
