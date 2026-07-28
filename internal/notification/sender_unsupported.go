//go:build !darwin

package notification

import "errors"

const Supported = false

func newSystemSender() Sender {
	return unavailable(errors.New("unsupported operating system"))
}
