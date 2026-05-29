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

	cron "github.com/netresearch/go-cron"
)

type fakeScheduler struct {
	mu        sync.Mutex
	spec      string
	job       func()
	started   bool
	addErr    error
	stopped   bool
	blockStop bool
}

func (f *fakeScheduler) AddFunc(spec string, cmd func(), _ ...cron.JobOption) (cron.EntryID, error) {
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
	if f.blockStop {
		return context.Background()
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

func assertExitCode(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("expected exit code %d, got %d", want, got)
	}
}

func assertLogContains(t *testing.T, buf *bytes.Buffer, substr string) {
	t.Helper()
	if !strings.Contains(buf.String(), substr) {
		t.Fatalf("unexpected logs: %q", buf.String())
	}
}

func TestBuildCommand(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		cmd, err := buildCommand(ctx, []string{"app", "echo", "hello"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cmd == nil {
			t.Fatal("expected command")
		}
	})

	t.Run("missing command uses argv name", func(t *testing.T) {
		_, err := buildCommand(ctx, []string{"app"})
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "usage: app") {
			t.Fatalf("expected usage to reference 'app', got %q", err.Error())
		}
	})

	t.Run("missing args defaults to docre", func(t *testing.T) {
		_, err := buildCommand(ctx, nil)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "usage: docre") {
			t.Fatalf("expected usage to default to 'docre', got %q", err.Error())
		}
	})
}

func TestRunCmd(t *testing.T) {
	t.Run("streams output on success", func(t *testing.T) {
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
	})

	t.Run("streams output and logs error on failure", func(t *testing.T) {
		orig := execCommand
		defer func() { execCommand = orig }()
		execCommand = helperExec("fail")

		logger, logBuf := newTestLogger()
		stdout := &bytes.Buffer{}
		runCmd(context.Background(), []string{"app", "dummy"}, logger, stdout, io.Discard)()

		if !strings.Contains(stdout.String(), "helper failure output") {
			t.Fatalf("expected failure output on stdout, got %q", stdout.String())
		}
		assertLogContains(t, logBuf, "ERROR: exit status 7")
	})

	t.Run("logs build command error", func(t *testing.T) {
		logger, buf := newTestLogger()
		runCmd(context.Background(), []string{"app"}, logger, io.Discard, io.Discard)()

		assertLogContains(t, buf, "usage: app <command> [args...]")
	})
}

func TestRun(t *testing.T) {
	t.Run("missing command", func(t *testing.T) {
		logger, buf := newTestLogger()
		code := run([]string{"app"}, envOnly("", ""), make(chan os.Signal, 1), &fakeScheduler{}, logger, io.Discard, io.Discard)

		assertExitCode(t, code, 1)
		assertLogContains(t, buf, "usage: app <command> [args...]")
	})

	t.Run("missing crontab", func(t *testing.T) {
		logger, buf := newTestLogger()
		code := run([]string{"app", "echo"}, envOnly("", ""), make(chan os.Signal, 1), &fakeScheduler{}, logger, io.Discard, io.Discard)

		assertExitCode(t, code, 1)
		assertLogContains(t, buf, "CRONTAB is required")
	})

	t.Run("invalid wait timeout", func(t *testing.T) {
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

		assertExitCode(t, code, 1)
		assertLogContains(t, buf, "invalid WAIT_TIMEOUT")
	})

	t.Run("add func error", func(t *testing.T) {
		logger, buf := newTestLogger()
		code := run(
			[]string{"app", "echo"},
			envOnly("CRONTAB", "* * * * *"),
			make(chan os.Signal, 1),
			&fakeScheduler{addErr: errors.New("bad cron")},
			logger,
			io.Discard, io.Discard,
		)

		assertExitCode(t, code, 1)
		assertLogContains(t, buf, "bad cron")
	})

	t.Run("clean shutdown", func(t *testing.T) {
		logger, buf := newTestLogger()
		sigChan := make(chan os.Signal, 1)
		s := &fakeScheduler{}

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
		assertExitCode(t, <-done, 0)

		if !s.started {
			t.Fatal("expected scheduler to start")
		}
		if !s.stopped {
			t.Fatal("expected scheduler to stop")
		}
		if s.job == nil {
			t.Fatal("expected job to be registered")
		}

		assertLogContains(t, buf, "Received signal: terminated")
		assertLogContains(t, buf, "All jobs completed. Exiting now.")
	})

	t.Run("timeout shutdown kills running job", func(t *testing.T) {
		logger, buf := newTestLogger()
		sigChan := make(chan os.Signal, 1)
		s := &fakeScheduler{blockStop: true}

		getenv := func(k string) string {
			switch k {
			case "CRONTAB":
				return "* * * * *"
			case "WAIT_TIMEOUT":
				return "10ms"
			}
			return ""
		}

		done := make(chan int, 1)
		go func() {
			done <- run(
				[]string{"app", "echo"},
				getenv,
				sigChan,
				s,
				logger,
				io.Discard, io.Discard,
			)
		}()

		sigChan <- syscall.SIGTERM
		assertExitCode(t, <-done, 0)
		assertLogContains(t, buf, "Wait timeout reached")
	})
}

func TestMainExits(t *testing.T) {
	origExit := osExit
	origArgs := os.Args
	origLogOutput := log.Default().Writer()
	defer func() {
		osExit = origExit
		os.Args = origArgs
		log.Default().SetOutput(origLogOutput)
	}()

	log.Default().SetOutput(io.Discard)

	var captured int
	osExit = func(code int) { captured = code }
	os.Args = []string{"docre"}

	main()

	assertExitCode(t, captured, 1)
}

func TestNewScheduler(t *testing.T) {
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
