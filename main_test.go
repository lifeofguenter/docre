package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	cron "github.com/netresearch/go-cron"
)

type schedEntry struct {
	spec string
	job  func()
}

type fakeScheduler struct {
	mu        sync.Mutex
	entries   []schedEntry
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
	f.entries = append(f.entries, schedEntry{spec: spec, job: cmd})
	return cron.EntryID(len(f.entries)), nil
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

func TestParseCrontabLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantSpec string
		wantArgs []string
	}{
		{"5-field", "0 * * * * echo hello world", "0 * * * *", []string{"echo", "hello", "world"}},
		{"@hourly", "@hourly /bin/run.sh", "@hourly", []string{"/bin/run.sh"}},
		{"@every", "@every 5m curl https://example.com", "@every 5m", []string{"curl", "https://example.com"}},
		{"extra whitespace", "  *  *  *  *  *   echo   hi  ", "* * * * *", []string{"echo", "hi"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j, err := parseCrontabLine(tc.line)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if j.spec != tc.wantSpec {
				t.Fatalf("spec: got %q, want %q", j.spec, tc.wantSpec)
			}
			if !slices.Equal(j.args, tc.wantArgs) {
				t.Fatalf("args: got %v, want %v", j.args, tc.wantArgs)
			}
		})
	}

	t.Run("missing command", func(t *testing.T) {
		_, err := parseCrontabLine("* * * * *")
		if err == nil || !strings.Contains(err.Error(), "missing command") {
			t.Fatalf("expected missing-command error, got %v", err)
		}
	})

	t.Run("@every missing duration", func(t *testing.T) {
		_, err := parseCrontabLine("@every 5m")
		if err == nil || !strings.Contains(err.Error(), "missing command") {
			t.Fatalf("expected missing-command error, got %v", err)
		}
	})

	t.Run("empty line", func(t *testing.T) {
		_, err := parseCrontabLine("   \t  ")
		if err == nil || !strings.Contains(err.Error(), "empty line") {
			t.Fatalf("expected empty-line error, got %v", err)
		}
	})
}

func TestLoadJobs(t *testing.T) {
	t.Run("argv command (old style)", func(t *testing.T) {
		jobs, err := loadJobs("* * * * *", []string{"app", "echo", "hi"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(jobs) != 1 {
			t.Fatalf("expected 1 job, got %d", len(jobs))
		}
		if jobs[0].spec != "* * * * *" {
			t.Fatalf("spec: %q", jobs[0].spec)
		}
		if !slices.Equal(jobs[0].args, []string{"echo", "hi"}) {
			t.Fatalf("args: %v", jobs[0].args)
		}
	})

	t.Run("argv command with empty CRONTAB", func(t *testing.T) {
		_, err := loadJobs("", []string{"app", "echo"})
		if err == nil || !strings.Contains(err.Error(), "CRONTAB is required") {
			t.Fatalf("expected CRONTAB-required error, got %v", err)
		}
	})

	t.Run("multi-line CRONTAB with comments and blanks", func(t *testing.T) {
		spec := "# every minute\n* * * * * echo a\n\n0 * * * * echo b\n# trailing comment\n"
		jobs, err := loadJobs(spec, []string{"app"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(jobs) != 2 {
			t.Fatalf("expected 2 jobs, got %d", len(jobs))
		}
		if jobs[0].spec != "* * * * *" || !slices.Equal(jobs[0].args, []string{"echo", "a"}) {
			t.Fatalf("job 0: %+v", jobs[0])
		}
		if jobs[1].spec != "0 * * * *" || !slices.Equal(jobs[1].args, []string{"echo", "b"}) {
			t.Fatalf("job 1: %+v", jobs[1])
		}
	})

	t.Run("no argv and no CRONTAB", func(t *testing.T) {
		_, err := loadJobs("", []string{"app"})
		if err == nil || !strings.Contains(err.Error(), "usage: app <command>") {
			t.Fatalf("expected usage error, got %v", err)
		}
	})

	t.Run("CRONTAB with only comments", func(t *testing.T) {
		_, err := loadJobs("# nothing here\n\n", []string{"app"})
		if err == nil || !strings.Contains(err.Error(), "CRONTAB has no jobs") {
			t.Fatalf("expected no-jobs error, got %v", err)
		}
	})

	t.Run("multi-line CRONTAB with invalid line", func(t *testing.T) {
		_, err := loadJobs("* * * * * echo a\n* * * * *\n", []string{"app"})
		if err == nil || !strings.Contains(err.Error(), "crontab line 2") {
			t.Fatalf("expected line-2 error, got %v", err)
		}
	})

	t.Run("usage defaults to docre when argv empty", func(t *testing.T) {
		_, err := loadJobs("", nil)
		if err == nil || !strings.Contains(err.Error(), "usage: docre") {
			t.Fatalf("expected docre usage error, got %v", err)
		}
	})
}

func TestRunJob(t *testing.T) {
	t.Run("streams output on success", func(t *testing.T) {
		orig := execCommand
		defer func() { execCommand = orig }()
		execCommand = helperExec("success")

		logger, logBuf := newTestLogger()
		stdout := &bytes.Buffer{}
		runJob(context.Background(), job{args: []string{"dummy"}}, logger, stdout, io.Discard)()

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
		runJob(context.Background(), job{args: []string{"dummy"}}, logger, stdout, io.Discard)()

		if !strings.Contains(stdout.String(), "helper failure output") {
			t.Fatalf("expected failure output on stdout, got %q", stdout.String())
		}
		assertLogContains(t, logBuf, "ERROR: exit status 7")
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

	t.Run("clean shutdown (old style)", func(t *testing.T) {
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
		if len(s.entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(s.entries))
		}
		if s.entries[0].spec != "* * * * *" {
			t.Fatalf("entry spec: %q", s.entries[0].spec)
		}

		assertLogContains(t, buf, "Received signal: terminated")
		assertLogContains(t, buf, "All jobs completed. Exiting now.")
	})

	t.Run("clean shutdown (multi-line CRONTAB)", func(t *testing.T) {
		logger, buf := newTestLogger()
		sigChan := make(chan os.Signal, 1)
		s := &fakeScheduler{}

		crontab := "* * * * * echo a\n0 * * * * echo b\n@every 30s echo c"

		done := make(chan int, 1)
		go func() {
			done <- run(
				[]string{"app"},
				envOnly("CRONTAB", crontab),
				sigChan,
				s,
				logger,
				io.Discard, io.Discard,
			)
		}()

		sigChan <- syscall.SIGTERM
		assertExitCode(t, <-done, 0)

		if len(s.entries) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(s.entries))
		}
		wantSpecs := []string{"* * * * *", "0 * * * *", "@every 30s"}
		for i, want := range wantSpecs {
			if s.entries[i].spec != want {
				t.Fatalf("entry %d spec: got %q, want %q", i, s.entries[i].spec, want)
			}
		}

		assertLogContains(t, buf, "All jobs completed. Exiting now.")
	})

	t.Run("multi-line CRONTAB jobs invoke their own commands", func(t *testing.T) {
		orig := execCommand
		defer func() { execCommand = orig }()

		var calls []string
		execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
			calls = append(calls, name)
			return helperExec("success")(ctx, name, args...)
		}

		jobs, err := loadJobs("* * * * * /bin/first\n0 * * * * /bin/second", []string{"app"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		logger, _ := newTestLogger()
		for _, j := range jobs {
			runJob(context.Background(), j, logger, io.Discard, io.Discard)()
		}

		if !slices.Equal(calls, []string{"/bin/first", "/bin/second"}) {
			t.Fatalf("expected /bin/first then /bin/second, got %v", calls)
		}
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
