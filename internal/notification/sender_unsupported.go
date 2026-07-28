//go:build !darwin

package notification

import "errors"

func newSystemSender() Sender {
	return unavailable(errors.New("unsupported operating system"))
}
