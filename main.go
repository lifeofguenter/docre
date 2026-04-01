package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
)

const WaitTimeout = 110 * time.Second

type scheduler interface {
	AddFunc(string, func()) (cron.EntryID, error)
	Start()
	Stop() context.Context
}

var execCommand = exec.Command

func buildCommand(args []string) (*exec.Cmd, error) {
	if len(args) < 2 {
		return nil, errors.New("usage: app <command> [args...]")
	}
	return execCommand(args[1], args[2:]...), nil
}

func runCmd(args []string, logger *log.Logger) func() {
	return func() {
		cmd, err := buildCommand(args)
		if err != nil {
			logger.Printf("ERROR: %v", err)
			return
		}

		output, err := cmd.CombinedOutput()
		if len(output) > 0 {
			logger.Printf("%s", output)
		}
		if err != nil {
			logger.Printf("ERROR: %v", err)
		}
	}
}

func newScheduler() scheduler {
	return cron.New(
		cron.WithChain(
			cron.Recover(cron.DefaultLogger),
			cron.SkipIfStillRunning(cron.DefaultLogger),
		),
	)
}

func run(args []string, getenv func(string) string, sigChan <-chan os.Signal, c scheduler, logger *log.Logger) int {
	if _, err := buildCommand(args); err != nil {
		logger.Printf("Error: %v", err)
		return 1
	}

	spec := getenv("CRONTAB")
	if spec == "" {
		logger.Printf("Error: CRONTAB is required")
		return 1
	}

	_, err := c.AddFunc(spec, runCmd(args, logger))
	if err != nil {
		logger.Printf("Error: %v", err)
		return 1
	}

	c.Start()

	sig := <-sigChan
	logger.Printf("Received signal: %s. Waiting for %s before exiting...", sig, WaitTimeout)

	stopCtx := c.Stop()

	select {
	case <-stopCtx.Done():
		logger.Println("All jobs completed. Exiting now.")
	case <-time.After(WaitTimeout):
		logger.Println("Wait timeout reached. Exiting now.")
	}

	return 0
}

func main() {
	logger := log.Default()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	os.Exit(run(os.Args, os.Getenv, sigChan, newScheduler(), logger))
}
