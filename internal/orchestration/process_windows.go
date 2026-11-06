//go:build windows

package orchestration

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// A reused PID is treated as live: safely detecting absence is more
// important than automatically unlocking an old task.
func previousProcessGone(pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("%w: old process PID is unknown", ErrConflict)
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return true, nil
		}
		return false, fmt.Errorf("%w: process %d cannot be checked: %v", ErrConflict, pid, err)
	}
	defer windows.CloseHandle(h)
	result, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return false, err
	}
	if result == windows.WAIT_OBJECT_0 {
		return true, nil
	}
	if result == uint32(windows.WAIT_TIMEOUT) {
		return false, nil
	}
	return false, fmt.Errorf("%w: unknown process wait result", ErrConflict)
}
