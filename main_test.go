package main

import (
	"bytes"
	"context"
	"errors"
	"io"
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

func envOnly(key, value string) func(string) string {
	return func(k string) string {
		if k == key {
			return value
		}
		return ""
	}
}

func helperExec(scenario string) func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", scenario}
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
	}
}

func TestBuildCommand_Success(t *testing.T) {
	cmd, err := buildCommand(context.Background(), []string{"app", "echo", "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected command")
	}
}

func TestBuildCommand_MissingCommand(t *testing.T) {
	_, err := buildCommand(context.Background(), []string{"app"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "usage: app") {
		t.Fatalf("expected usage to reference 'app', got %q", err.Error())
	}
}

func TestBuildCommand_DefaultName(t *testing.T) {
	_, err := buildCommand(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "usage: docre") {
		t.Fatalf("expected usage to default to 'docre', got %q", err.Error())
	}
}

func TestRunCmd_StreamsOutputOnSuccess(t *testing.T) {
	orig := execCommand
	defer func() { execCommand = orig }()
	execCommand = helperExec("success")

	logger, logBuf := newTestLogger()
	stdout := &bytes.Buffer{}
	runCmd(context.Background(), []string{"app", "dummy"}, logger, stdout, io.Discard)()

	if !strings.Contains(stdout.String(), "helper success output") {
		t.Fatalf("expected success output on stdout, got %q", stdout.String())
	}
	if logBuf.Len() != 0 {
		t.Fatalf("expected no log output on success, got %q", logBuf.String())
	}
}

func TestRunCmd_StreamsOutputAndLogsErrorOnFailure(t *testing.T) {
	orig := execCommand
	defer func() { execCommand = orig }()
	execCommand = helperExec("fail")

	logger, logBuf := newTestLogger()
	stdout := &bytes.Buffer{}
	runCmd(context.Background(), []string{"app", "dummy"}, logger, stdout, io.Discard)()

	if !strings.Contains(stdout.String(), "helper failure output") {
		t.Fatalf("expected failure output on stdout, got %q", stdout.String())
	}
	if !strings.Contains(logBuf.String(), "ERROR: exit status 7") {
		t.Fatalf("expected exit status in logger, got %q", logBuf.String())
	}
}

func TestRunCmd_LogsBuildCommandError(t *testing.T) {
	logger, buf := newTestLogger()
	runCmd(context.Background(), []string{"app"}, logger, io.Discard, io.Discard)()

	if !strings.Contains(buf.String(), "usage: app <command> [args...]") {
		t.Fatalf("expected usage error in logs, got %q", buf.String())
	}
}

func TestRun_MissingCommand(t *testing.T) {
	logger, buf := newTestLogger()
	code := run([]string{"app"}, func(string) string { return "" }, make(chan os.Signal, 1), &fakeScheduler{}, logger, io.Discard, io.Discard)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "usage: app <command> [args...]") {
		t.Fatalf("unexpected logs: %q", buf.String())
	}
}

func TestRun_MissingCrontab(t *testing.T) {
	logger, buf := newTestLogger()
	code := run([]string{"app", "echo"}, func(string) string { return "" }, make(chan os.Signal, 1), &fakeScheduler{}, logger, io.Discard, io.Discard)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "CRONTAB is required") {
		t.Fatalf("unexpected logs: %q", buf.String())
	}
}

func TestRun_InvalidWaitTimeout(t *testing.T) {
	logger, buf := newTestLogger()
	getenv := func(k string) string {
		switch k {
		case "CRONTAB":
			return "* * * * *"
		case "WAIT_TIMEOUT":
			return "not-a-duration"
		}
		return ""
	}
	code := run([]string{"app", "echo"}, getenv, make(chan os.Signal, 1), &fakeScheduler{}, logger, io.Discard, io.Discard)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "invalid WAIT_TIMEOUT") {
		t.Fatalf("unexpected logs: %q", buf.String())
	}
}

func TestRun_AddFuncError(t *testing.T) {
	logger, buf := newTestLogger()
	sigChan := make(chan os.Signal, 1)

	code := run(
		[]string{"app", "echo"},
		envOnly("CRONTAB", "* * * * *"),
		sigChan,
		&fakeScheduler{addErr: errors.New("bad cron")},
		logger,
		io.Discard, io.Discard,
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
			envOnly("CRONTAB", "* * * * *"),
			sigChan,
			s,
			logger,
			io.Discard, io.Discard,
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

func TestNewScheduler_AddsJob(t *testing.T) {
	logger, _ := newTestLogger()
	s := newScheduler(logger)
	if s == nil {
		t.Fatal("expected scheduler")
	}
	id, err := s.AddFunc("* * * * *", func() {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero entry id")
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
