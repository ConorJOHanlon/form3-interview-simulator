//go:build integration && !windows

package main

// These tests run the real entry point in a separate process, to cover what
// in-process tests can't: command-line handling, exit codes and OS signals.

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	childEnv            = "SIMULATOR_TEST_CHILD"
	childAddrEnv        = "SIMULATOR_TEST_ADDR"
	childGracePeriodEnv = "SIMULATOR_TEST_GRACE_PERIOD"

	// processTimeout bounds every wait on a child process.
	processTimeout = 3 * time.Second
)

// TestMain runs the simulator instead of the tests when started by launch.
// The test binary stands in for the simulator binary, so the configuration
// struct can be set without adding flags to the real one.
func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "" {
		os.Exit(m.Run())
	}

	gracePeriod, err := time.ParseDuration(os.Getenv(childGracePeriodEnv))
	if err != nil {
		panic(err)
	}
	config.Addr = os.Getenv(childAddrEnv)
	config.GracePeriod = gracePeriod
	main()
	os.Exit(0)
}

func TestProcess(t *testing.T) {
	t.Run("handles the command line", func(t *testing.T) {
		tests := map[string]struct {
			args             []string
			expectedExitCode int
			expectedOutput   string
		}{
			"prints usage with -h":    {args: []string{"-h"}, expectedExitCode: 0, expectedOutput: "USAGE"},
			"rejects an unknown flag": {args: []string{"-unknown"}, expectedExitCode: 2, expectedOutput: "flag provided but not defined"},
			"rejects an argument":     {args: []string{"unexpected"}, expectedExitCode: 2, expectedOutput: "unexpected argument"},
		}

		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				process := launch(t, "127.0.0.1:0", time.Second, tt.args...)

				process.requireExitCode(t, tt.expectedExitCode)
				assert.Contains(t, process.output.String(), tt.expectedOutput)
			})
		}
	})

	t.Run("exits with an error when the address is in use", func(t *testing.T) {
		t.Parallel()
		taken, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer taken.Close()

		process := launch(t, taken.Addr().String(), time.Second)

		process.requireExitCode(t, 1)
		assert.Contains(t, process.output.String(), "address already in use")
	})

	for name, sig := range map[string]syscall.Signal{"SIGINT": syscall.SIGINT, "SIGTERM": syscall.SIGTERM} {
		t.Run("cancels unfinished payments and exits cleanly on "+name, func(t *testing.T) {
			t.Parallel()
			const gracePeriod = 300 * time.Millisecond
			process, addr := start(t, gracePeriod)
			client := connect(t, addr)
			client.Send("PAYMENT|5000")

			signalled := time.Now()
			process.signal(t, sig)

			assert.Equal(t, "RESPONSE|REJECTED|Cancelled", client.Receive())
			assert.GreaterOrEqual(t, time.Since(signalled), gracePeriod, "cancelled before the grace period expired")
			client.RequireClosed()
			process.requireExitCode(t, 0)
		})

		t.Run("exits immediately on a second "+name, func(t *testing.T) {
			t.Parallel()
			process, addr := start(t, time.Minute)
			connect(t, addr) // An idle connection holds shutdown open.

			process.signal(t, sig)
			process.waitForOutput(t, "send it again to exit immediately")
			process.signal(t, sig)

			var exitErr *exec.ExitError
			require.ErrorAs(t, process.wait(t), &exitErr)
			status := exitErr.Sys().(syscall.WaitStatus)
			assert.True(t, status.Signaled() && status.Signal() == sig, "process was not killed by %s: %v", name, exitErr)
		})
	}
}

// process is a simulator running in a child process.
type process struct {
	cmd    *exec.Cmd
	output *output
	exited chan struct{}
	err    error // Valid once exited is closed.
}

// launch starts the simulator listening on addr with the given arguments. The
// process is killed if it is still running when the test ends.
func launch(t *testing.T, addr string, gracePeriod time.Duration, args ...string) *process {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(),
		childEnv+"=1",
		childAddrEnv+"="+addr,
		childGracePeriodEnv+"="+gracePeriod.String(),
		// The race detector otherwise waits a second before exiting.
		"GORACE=atexit_sleep_ms=0",
	)
	p := &process{cmd: cmd, output: newOutput(), exited: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = p.output, p.output
	require.NoError(t, cmd.Start())
	go func() {
		p.err = cmd.Wait()
		close(p.exited)
	}()

	t.Cleanup(func() {
		cmd.Process.Kill()
		<-p.exited
		if t.Failed() {
			t.Logf("simulator output:\n%s", p.output)
		}
	})
	return p
}

var listeningAddr = regexp.MustCompile(`msg=listening addr=(\S+)`)

// start launches the simulator on a random port and returns its address once
// it is listening.
func start(t *testing.T, gracePeriod time.Duration) (*process, string) {
	t.Helper()
	p := launch(t, "127.0.0.1:0", gracePeriod)
	p.waitForOutput(t, "msg=listening")
	match := listeningAddr.FindStringSubmatch(p.output.String())
	require.NotNil(t, match, "startup log has no address")
	return p, match[1]
}

func (p *process) signal(t *testing.T, sig os.Signal) {
	t.Helper()
	require.NoError(t, p.cmd.Process.Signal(sig))
}

func (p *process) waitForOutput(t *testing.T, text string) {
	t.Helper()
	timeout := time.After(processTimeout)
	for !bytes.Contains(p.output.Bytes(), []byte(text)) {
		select {
		case <-p.output.written:
		case <-p.exited:
			t.Fatalf("simulator exited before writing %q", text)
		case <-timeout:
			t.Fatalf("simulator did not write %q within %v", text, processTimeout)
		}
	}
}

func (p *process) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-p.exited:
		return p.err
	case <-time.After(processTimeout):
		t.Fatalf("simulator did not exit within %v", processTimeout)
		return nil
	}
}

func (p *process) requireExitCode(t *testing.T, code int) {
	t.Helper()
	err := p.wait(t)
	exitCode := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}
	assert.Equal(t, code, exitCode)
}

// output collects a process's output and signals each write.
type output struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	written chan struct{}
}

func newOutput() *output {
	return &output{written: make(chan struct{}, 1)}
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n, err := o.buf.Write(p)
	select {
	case o.written <- struct{}{}:
	default:
	}
	return n, err
}

func (o *output) Bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return bytes.Clone(o.buf.Bytes())
}

func (o *output) String() string {
	return string(o.Bytes())
}
