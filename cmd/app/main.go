package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	platformruntime "github.com/upcomers-org/platformgo/internal/runtime"
)

type command struct {
	name     string
	handlers []string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	parsed, err := parseCLI(arguments)
	if err != nil {
		return err
	}
	if parsed.name == "help" {
		_, _ = fmt.Fprint(os.Stdout, helpText)
		return nil
	}
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
	switch parsed.name {
	case "serve":
		return platformruntime.Serve(ctx, config)
	case "worker":
		return platformruntime.RunWorkers(ctx, config, parsed.handlers)
	case "migrate":
		return platformruntime.Migrate(ctx, config)
	case "doctor":
		return platformruntime.Doctor(ctx, config)
	default:
		return fmt.Errorf("unknown command %q", parsed.name)
	}
}

func parseCLI(arguments []string) (command, error) {
	if len(arguments) == 0 ||
		arguments[0] == "help" ||
		arguments[0] == "--help" ||
		arguments[0] == "-h" {
		return command{name: "help"}, nil
	}
	parsed := command{name: arguments[0]}
	switch parsed.name {
	case "serve", "migrate", "doctor":
		if len(arguments) != 1 {
			return command{}, fmt.Errorf("%s takes no arguments", parsed.name)
		}
	case "worker":
		for index := 1; index < len(arguments); index++ {
			value := arguments[index]
			switch {
			case value == "--handlers":
				index++
				if index >= len(arguments) {
					return command{}, errors.New("--handlers expects a value")
				}
				parsed.handlers = append(parsed.handlers, arguments[index])
			case strings.HasPrefix(value, "--handlers="):
				parsed.handlers = append(
					parsed.handlers,
					strings.TrimPrefix(value, "--handlers="),
				)
			default:
				return command{}, fmt.Errorf("unknown worker option %q", value)
			}
		}
		if len(parsed.handlers) == 0 {
			return command{}, errors.New("worker requires --handlers=<role>")
		}
	default:
		return command{}, fmt.Errorf("unknown command %q", parsed.name)
	}
	return parsed, nil
}

const helpText = `Usage:
  app serve
  app worker --handlers=<role>
  app migrate
  app doctor

Worker roles:
  outbox-publisher
  event-consumer
`
