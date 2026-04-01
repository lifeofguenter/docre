package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/robfig/cron/v3"
)

type fakeScheduler struct {
	mu      sync.Mutex
	spec    string
	job     func()
	started bool
	stopCtx context.Context
	addErr  error
	stopped bool
}

func (f *fakeScheduler) AddFunc(spec string, cmd func()) (cron.EntryID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.addErr != nil {
		return 0, f.addErr
	}
	f.spec = spec
	f.job = cmd
	return 1, nil
}

func (f *fakeScheduler) Start() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
}

func (f *fakeScheduler) Stop() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	if f.stopCtx != nil {
		return f.stopCtx
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func newTestLogger() (*log.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return log.New(buf, "", 0), buf
}

func TestBuildCommand_Success(t *testing.T) {
	cmd, err := buildCommand([]string{"app", "echo", "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected command")
	}
}

func TestBuildCommand_MissingCommand(t *testing.T) {
	_, err := buildCommand([]string{"app"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunCmd_LogsOutputOnSuccess(t *testing.T) {
	origExecCommand := execCommand
	defer func() { execCommand = origExecCommand }()

	execCommand = func(name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", "success"}
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
	}

	logger, buf := newTestLogger()
	runCmd([]string{"app", "dummy"}, logger)()

	logs := buf.String()
	if !strings.Contains(logs, "helper success output") {
		t.Fatalf("expected success output in logs, got %q", logs)
	}
}

func TestRunCmd_LogsOutputAndErrorOnFailure(t *testing.T) {
	origExecCommand := execCommand
	defer func() { execCommand = origExecCommand }()

	execCommand = func(name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", "fail"}
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
	}

	logger, buf := newTestLogger()
	runCmd([]string{"app", "dummy"}, logger)()

	logs := buf.String()
	if !strings.Contains(logs, "helper failure output") {
		t.Fatalf("expected failure output in logs, got %q", logs)
	}
	if !strings.Contains(logs, "ERROR: exit status 7") {
		t.Fatalf("expected error in logs, got %q", logs)
	}
}

func TestRunCmd_LogsBuildCommandError(t *testing.T) {
	logger, buf := newTestLogger()
	runCmd([]string{"app"}, logger)()

	logs := buf.String()
	if !strings.Contains(logs, "usage: app <command> [args...]") {
		t.Fatalf("expected usage error in logs, got %q", logs)
	}
}

func TestRun_MissingCommand(t *testing.T) {
	logger, buf := newTestLogger()
	code := run([]string{"app"}, func(string) string { return "" }, make(chan os.Signal), &fakeScheduler{}, logger)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "usage: app <command> [args...]") {
		t.Fatalf("unexpected logs: %q", buf.String())
	}
}

func TestRun_MissingCrontab(t *testing.T) {
	logger, buf := newTestLogger()
	code := run([]string{"app", "echo"}, func(string) string { return "" }, make(chan os.Signal), &fakeScheduler{}, logger)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "CRONTAB is required") {
		t.Fatalf("unexpected logs: %q", buf.String())
	}
}

func TestRun_AddFuncError(t *testing.T) {
	logger, buf := newTestLogger()
	sigChan := make(chan os.Signal, 1)

	code := run(
		[]string{"app", "echo"},
		func(string) string { return "* * * * *" },
		sigChan,
		&fakeScheduler{addErr: errors.New("bad cron")},
		logger,
	)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "bad cron") {
		t.Fatalf("unexpected logs: %q", buf.String())
	}
}

func TestRun_CleanShutdown(t *testing.T) {
	logger, buf := newTestLogger()
	sigChan := make(chan os.Signal, 1)

	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()

	s := &fakeScheduler{stopCtx: stopCtx}

	done := make(chan int, 1)
	go func() {
		done <- run(
			[]string{"app", "echo"},
			func(string) string { return "* * * * *" },
			sigChan,
			s,
			logger,
		)
	}()

	sigChan <- syscall.SIGTERM

	code := <-done
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	if !s.started {
		t.Fatal("expected scheduler to start")
	}
	if !s.stopped {
		t.Fatal("expected scheduler to stop")
	}
	if s.job == nil {
		t.Fatal("expected job to be registered")
	}

	logs := buf.String()
	if !strings.Contains(logs, "Received signal: terminated") {
		t.Fatalf("unexpected logs: %q", logs)
	}
	if !strings.Contains(logs, "All jobs completed. Exiting now.") {
		t.Fatalf("unexpected logs: %q", logs)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	args := os.Args
	for i := range args {
		if args[i] == "--" && i+1 < len(args) {
			switch args[i+1] {
			case "success":
				_, _ = os.Stdout.WriteString("helper success output\n")
				os.Exit(0)
			case "fail":
				_, _ = os.Stdout.WriteString("helper failure output\n")
				os.Exit(7)
			}
		}
	}

	os.Exit(2)
}
