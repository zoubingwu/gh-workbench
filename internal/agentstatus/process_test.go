package agentstatus

import (
	"os"
	"testing"
)

func TestProcessIsAliveRecognizesCurrentProcess(t *testing.T) {
	t.Parallel()

	if !processIsAlive(os.Getpid()) {
		t.Fatal("current process reported as stopped")
	}
}
