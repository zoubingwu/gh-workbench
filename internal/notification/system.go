package notification

func SystemSender() Sender {
	return newSystemSender()
}
