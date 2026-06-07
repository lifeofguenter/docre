package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	cron "github.com/netresearch/go-cron"
)

const (
	DefaultWaitTimeout = 110 * time.Second
	errLogFmt          = "ERROR: %v"
)

type scheduler interface {
	AddFunc(string, func(), ...cron.JobOption) (cron.EntryID, error)
	Start()
	Stop() context.Context
}

type job struct {
	spec string
	args []string
}

var (
	execCommand = exec.CommandContext
	osExit      = os.Exit
)

func usageError(argv0 string) error {
	name := "docre"
	if argv0 != "" {
		name = filepath.Base(argv0)
	}
	return fmt.Errorf("usage: %s <command> [args...]", name)
}

func parseCrontabLine(line string) (job, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return job{}, errors.New("empty line")
	}

	var specFields int
	switch {
	case fields[0] == "@every":
		specFields = 2
	case strings.HasPrefix(fields[0], "@"):
		specFields = 1
	default:
		specFields = 5
	}

	if len(fields) <= specFields {
		return job{}, errors.New("missing command")
	}

	return job{
		spec: strings.Join(fields[:specFields], " "),
		args: fields[specFields:],
	}, nil
}

func loadJobs(crontab string, argv []string) ([]job, error) {
	if len(argv) >= 2 {
		spec := strings.TrimSpace(crontab)
		if spec == "" {
			return nil, errors.New("CRONTAB is required")
		}
		return []job{{spec: spec, args: argv[1:]}}, nil
	}

	var argv0 string
	if len(argv) > 0 {
		argv0 = argv[0]
	}
	if strings.TrimSpace(crontab) == "" {
		return nil, usageError(argv0)
	}

	var jobs []job
	for i, raw := range strings.Split(crontab, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		j, err := parseCrontabLine(line)
		if err != nil {
			return nil, fmt.Errorf("crontab line %d: %w", i+1, err)
		}
		jobs = append(jobs, j)
	}
	if len(jobs) == 0 {
		return nil, errors.New("CRONTAB has no jobs")
	}
	return jobs, nil
}

func runJob(ctx context.Context, j job, logger *log.Logger, stdout, stderr io.Writer) func() {
	return func() {
		cmd := execCommand(ctx, j.args[0], j.args[1:]...)
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

	jobs, err := loadJobs(getenv("CRONTAB"), args)
	if err != nil {
		logger.Printf(errLogFmt, err)
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

	for _, j := range jobs {
		if _, err := c.AddFunc(j.spec, runJob(runCtx, j, logger, stdout, stderr)); err != nil {
			logger.Printf(errLogFmt, err)
			return 1
		}
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
