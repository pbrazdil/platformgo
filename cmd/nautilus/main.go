package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	platformruntime "github.com/upcomers-org/platformgo/internal/runtime"
)

// nautilus preserves the legacy deployment binary name while routing to the
// Go single-writer engine consumer.
func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	config, err := platformruntime.LoadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	return platformruntime.RunWorkers(
		ctx,
		config,
		[]string{"event-consumer"},
	)
}
