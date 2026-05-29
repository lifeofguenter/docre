package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	DefaultWaitTimeout = 110 * time.Second
	errLogFmt          = "ERROR: %v"
)

type scheduler interface {
	AddFunc(string, func()) (cron.EntryID, error)
	Start()
	Stop() context.Context
}

var (
	execCommand = exec.CommandContext
	osExit      = os.Exit
)

func buildCommand(ctx context.Context, args []string) (*exec.Cmd, error) {
	if len(args) < 2 {
		name := "docre"
		if len(args) > 0 && args[0] != "" {
			name = filepath.Base(args[0])
		}
		return nil, fmt.Errorf("usage: %s <command> [args...]", name)
	}
	return execCommand(ctx, args[1], args[2:]...), nil
}

func runCmd(ctx context.Context, args []string, logger *log.Logger, stdout, stderr io.Writer) func() {
	return func() {
		cmd, err := buildCommand(ctx, args)
		if err != nil {
			logger.Printf(errLogFmt, err)
			return
		}

		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			logger.Printf(errLogFmt, err)
		}
	}
}

func newScheduler(logger *log.Logger) scheduler {
	cronLogger := cron.PrintfLogger(logger)
	return cron.New(
		cron.WithChain(
			cron.Recover(cronLogger),
			cron.SkipIfStillRunning(cronLogger),
		),
	)
}

func run(args []string, getenv func(string) string, sigChan <-chan os.Signal, c scheduler, logger *log.Logger, stdout, stderr io.Writer) int {
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	if _, err := buildCommand(runCtx, args); err != nil {
		logger.Printf(errLogFmt, err)
		return 1
	}

	spec := getenv("CRONTAB")
	if spec == "" {
		logger.Printf("ERROR: CRONTAB is required")
		return 1
	}

	waitTimeout := DefaultWaitTimeout
	if v := getenv("WAIT_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			logger.Printf("ERROR: invalid WAIT_TIMEOUT %q: %v", v, err)
			return 1
		}
		waitTimeout = d
	}

	if _, err := c.AddFunc(spec, runCmd(runCtx, args, logger, stdout, stderr)); err != nil {
		logger.Printf(errLogFmt, err)
		return 1
	}

	c.Start()

	sig := <-sigChan
	logger.Printf("Received signal: %s. Waiting for %s before exiting...", sig, waitTimeout)

	stopCtx := c.Stop()

	select {
	case <-stopCtx.Done():
		logger.Println("All jobs completed. Exiting now.")
	case <-time.After(waitTimeout):
		logger.Println("Wait timeout reached. Killing running job and exiting now.")
		cancelRun()
	}

	return 0
}

func main() {
	logger := log.Default()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigChan)

	osExit(run(os.Args, os.Getenv, sigChan, newScheduler(logger), logger, os.Stdout, os.Stderr))
}
