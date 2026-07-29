//go:build windows

package agentstatus

import (
	"errors"
	"syscall"
)

func processIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(
		syscall.SYNCHRONIZE,
		false,
		uint32(pid),
	)
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer func() {
		_ = syscall.CloseHandle(handle)
	}()

	result, err := syscall.WaitForSingleObject(handle, 0)
	return err == nil && result == syscall.WAIT_TIMEOUT
}
