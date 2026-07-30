//go:build !darwin

package notification

import (
	"context"
	"errors"
)

const Supported = false

type unavailableSender struct {
	err error
}

func (s *unavailableSender) Send(
	_ context.Context,
	_ Message,
) error {
	return s.err
}

func newSystemSender() Sender {
	return unavailable(errors.New("unsupported operating system"))
}

func unavailable(err error) Sender {
	return &unavailableSender{err: err}
}
