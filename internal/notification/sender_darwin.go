//go:build darwin

package notification

import (
	"context"
	"fmt"
	"os/exec"
)

const appleScript = `on run argv
	display notification (item 2 of argv) with title (item 1 of argv)
end run`

type darwinSender struct{}

func newSystemSender() Sender {
	return &darwinSender{}
}

func (s *darwinSender) Send(
	ctx context.Context,
	message Message,
) error {
	command := exec.CommandContext(
		ctx,
		"/usr/bin/osascript",
		"-e",
		appleScript,
		"--",
		message.Title,
		message.Body,
	)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf(
			"run osascript: %w: %s",
			err,
			string(output),
		)
	}
	return nil
}
