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

func multiEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
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

func assertErrContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got %v", substr, err)
	}
}

func assertJob(t *testing.T, j job, wantSpec string, wantArgs []string) {
	t.Helper()
	if j.spec != wantSpec {
		t.Fatalf("spec: got %q, want %q", j.spec, wantSpec)
	}
	if !slices.Equal(j.args, wantArgs) {
		t.Fatalf("args: got %v, want %v", j.args, wantArgs)
	}
}

func runAsync(args []string, getenv func(string) string, sigChan chan os.Signal, s scheduler, logger *log.Logger) <-chan int {
	done := make(chan int, 1)
	go func() {
		done <- run(args, getenv, sigChan, s, logger, io.Discard, io.Discard)
	}()
	return done
}

func runParseSuccess(t *testing.T, line, wantSpec string, wantArgs []string) {
	t.Helper()
	j, err := parseCrontabLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJob(t, j, wantSpec, wantArgs)
}

func runParseError(t *testing.T, line, wantErr string) {
	t.Helper()
	_, err := parseCrontabLine(line)
	assertErrContains(t, err, wantErr)
}

func TestParseCrontabLine(t *testing.T) {
	successCases := []struct {
		name, line, wantSpec string
		wantArgs             []string
	}{
		{"5-field", "0 * * * * echo hello world", "0 * * * *", []string{"echo", "hello", "world"}},
		{"@hourly", "@hourly /bin/run.sh", "@hourly", []string{"/bin/run.sh"}},
		{"@every", "@every 5m curl https://example.com", "@every 5m", []string{"curl", "https://example.com"}},
		{"extra whitespace", "  *  *  *  *  *   echo   hi  ", "* * * * *", []string{"echo", "hi"}},
	}
	for _, tc := range successCases {
		t.Run(tc.name, func(t *testing.T) {
			runParseSuccess(t, tc.line, tc.wantSpec, tc.wantArgs)
		})
	}

	errorCases := []struct {
		name, line, wantErr string
	}{
		{"missing command", "* * * * *", "missing command"},
		{"@every missing duration", "@every 5m", "missing command"},
		{"empty line", "   \t  ", "empty line"},
	}
	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			runParseError(t, tc.line, tc.wantErr)
		})
	}
}

func runLoadJobsSuccess(t *testing.T, crontab string, argv []string, wantJobs []job) {
	t.Helper()
	jobs, err := loadJobs(crontab, argv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != len(wantJobs) {
		t.Fatalf("expected %d jobs, got %d", len(wantJobs), len(jobs))
	}
	for i, want := range wantJobs {
		assertJob(t, jobs[i], want.spec, want.args)
	}
}

func TestLoadJobs(t *testing.T) {
	t.Run("success", testLoadJobsSuccess)
	t.Run("errors", testLoadJobsErrors)
}

func testLoadJobsSuccess(t *testing.T) {
	cases := []struct {
		name     string
		crontab  string
		argv     []string
		wantJobs []job
	}{
		{
			"argv command (old style)",
			"* * * * *",
			[]string{"app", "echo", "hi"},
			[]job{{spec: "* * * * *", args: []string{"echo", "hi"}}},
		},
		{
			"multi-line CRONTAB with comments and blanks",
			"# every minute\n* * * * * echo a\n\n0 * * * * echo b\n# trailing comment\n",
			[]string{"app"},
			[]job{
				{spec: "* * * * *", args: []string{"echo", "a"}},
				{spec: "0 * * * *", args: []string{"echo", "b"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runLoadJobsSuccess(t, tc.crontab, tc.argv, tc.wantJobs)
		})
	}
}

func testLoadJobsErrors(t *testing.T) {
	cases := []struct {
		name, crontab, wantErr string
		argv                   []string
	}{
		{"argv command with empty CRONTAB", "", "CRONTAB is required", []string{"app", "echo"}},
		{"no argv and no CRONTAB", "", "usage: app <command>", []string{"app"}},
		{"CRONTAB with only comments", "# nothing here\n\n", "CRONTAB has no jobs", []string{"app"}},
		{"multi-line CRONTAB with invalid line", "* * * * * echo a\n* * * * *\n", "crontab line 2", []string{"app"}},
		{"usage defaults to docre when argv empty", "", "usage: docre", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadJobs(tc.crontab, tc.argv)
			assertErrContains(t, err, tc.wantErr)
		})
	}
}

func runJobWithExec(t *testing.T, stub func(ctx context.Context, name string, args ...string) *exec.Cmd, wantStdoutSub, wantLogSub string) {
	t.Helper()
	orig := execCommand
	defer func() { execCommand = orig }()
	execCommand = stub

	logger, logBuf := newTestLogger()
	stdout := &bytes.Buffer{}
	runJob(context.Background(), job{args: []string{"dummy"}}, logger, stdout, io.Discard)()

	if !strings.Contains(stdout.String(), wantStdoutSub) {
		t.Fatalf("stdout: got %q, want substring %q", stdout.String(), wantStdoutSub)
	}
	if wantLogSub == "" {
		if logBuf.Len() != 0 {
			t.Fatalf("expected no log output, got %q", logBuf.String())
		}
		return
	}
	assertLogContains(t, logBuf, wantLogSub)
}

func TestRunJob(t *testing.T) {
	t.Run("streams output on success", func(t *testing.T) {
		runJobWithExec(t, helperExec("success"), "helper success output", "")
	})
	t.Run("streams output and logs error on failure", func(t *testing.T) {
		runJobWithExec(t, helperExec("fail"), "helper failure output", "ERROR: exit status 7")
	})
}

func TestRun(t *testing.T) {
	t.Run("errors", testRunErrors)
	t.Run("clean shutdown (old style)", testRunCleanShutdownOldStyle)
	t.Run("clean shutdown (multi-line CRONTAB)", testRunCleanShutdownMultiLine)
	t.Run("multi-line CRONTAB jobs invoke their own commands", testRunMultiLineJobsInvoke)
	t.Run("timeout shutdown kills running job", testRunTimeoutShutdown)
}

func testRunErrors(t *testing.T) {
	cases := []struct {
		name    string
		argv    []string
		getenv  func(string) string
		sched   *fakeScheduler
		wantSub string
	}{
		{"missing command", []string{"app"}, envOnly("", ""), &fakeScheduler{}, "usage: app <command> [args...]"},
		{"missing crontab", []string{"app", "echo"}, envOnly("", ""), &fakeScheduler{}, "CRONTAB is required"},
		{"invalid wait timeout", []string{"app", "echo"}, multiEnv(map[string]string{"CRONTAB": "* * * * *", "WAIT_TIMEOUT": "not-a-duration"}), &fakeScheduler{}, "invalid WAIT_TIMEOUT"},
		{"add func error", []string{"app", "echo"}, envOnly("CRONTAB", "* * * * *"), &fakeScheduler{addErr: errors.New("bad cron")}, "bad cron"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger, buf := newTestLogger()
			code := run(tc.argv, tc.getenv, make(chan os.Signal, 1), tc.sched, logger, io.Discard, io.Discard)
			assertExitCode(t, code, 1)
			assertLogContains(t, buf, tc.wantSub)
		})
	}
}

func testRunCleanShutdownOldStyle(t *testing.T) {
	logger, buf := newTestLogger()
	sigChan := make(chan os.Signal, 1)
	s := &fakeScheduler{}

	done := runAsync([]string{"app", "echo"}, envOnly("CRONTAB", "* * * * *"), sigChan, s, logger)

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
}

func testRunCleanShutdownMultiLine(t *testing.T) {
	logger, buf := newTestLogger()
	sigChan := make(chan os.Signal, 1)
	s := &fakeScheduler{}

	crontab := "* * * * * echo a\n0 * * * * echo b\n@every 30s echo c"
	done := runAsync([]string{"app"}, envOnly("CRONTAB", crontab), sigChan, s, logger)

	sigChan <- syscall.SIGTERM
	assertExitCode(t, <-done, 0)

	wantSpecs := []string{"* * * * *", "0 * * * *", "@every 30s"}
	if len(s.entries) != len(wantSpecs) {
		t.Fatalf("expected %d entries, got %d", len(wantSpecs), len(s.entries))
	}
	for i, want := range wantSpecs {
		if s.entries[i].spec != want {
			t.Fatalf("entry %d spec: got %q, want %q", i, s.entries[i].spec, want)
		}
	}

	assertLogContains(t, buf, "All jobs completed. Exiting now.")
}

func testRunMultiLineJobsInvoke(t *testing.T) {
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
}

func testRunTimeoutShutdown(t *testing.T) {
	logger, buf := newTestLogger()
	sigChan := make(chan os.Signal, 1)
	s := &fakeScheduler{blockStop: true}

	getenv := multiEnv(map[string]string{
		"CRONTAB":      "* * * * *",
		"WAIT_TIMEOUT": "10ms",
	})

	done := runAsync([]string{"app", "echo"}, getenv, sigChan, s, logger)

	sigChan <- syscall.SIGTERM
	assertExitCode(t, <-done, 0)
	assertLogContains(t, buf, "Wait timeout reached")
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
