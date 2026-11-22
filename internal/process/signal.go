package process

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func StopContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
