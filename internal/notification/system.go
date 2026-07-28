package notification

import (
	"context"
)

func SystemSender() Sender {
	return newSystemSender()
}

type unavailableSender struct {
	err error
}

func (s *unavailableSender) Send(
	_ context.Context,
	_ Message,
) error {
	return s.err
}

func unavailable(err error) Sender {
	return &unavailableSender{err: err}
}
