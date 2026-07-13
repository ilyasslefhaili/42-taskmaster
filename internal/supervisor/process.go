package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"42-taskmaster/internal/config"
)

// State is the lifecycle state of a single supervised process. The values and
// transitions follow supervisor's process states.
type State int

const (
	// Stopped: not running and not scheduled to run.
	Stopped State = iota
	// Starting: launched, waiting to stay up for starttime seconds.
	Starting
	// Running: has stayed up past starttime; considered successfully started.
	Running
	// Backoff: a start attempt failed and a retry is scheduled.
	Backoff
	// Stopping: a stop was requested; awaiting exit or the kill deadline.
	Stopping
	// Exited: ran successfully then exited and was not restarted.
	Exited
	// Fatal: could not be started after startretries attempts.
	Fatal
)

// String returns the uppercase state name used in status output and logs.
func (s State) String() string {
	switch s {
	case Stopped:
		return "STOPPED"
	case Starting:
		return "STARTING"
	case Running:
		return "RUNNING"
	case Backoff:
		return "BACKOFF"
	case Stopping:
		return "STOPPING"
	case Exited:
		return "EXITED"
	case Fatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// process is one running (or runnable) instance of a program.
type process struct {
	name string          // "program" or "program:index" when numprocs > 1
	prog *config.Program // shared program spec

	state   State
	cmd     *exec.Cmd
	pid     int
	runID   int // increments per launch; guards stale timer/exit events
	retries int // consecutive failed start attempts

	startedAt          time.Time
	lastExitCode       int
	lastExitedBySignal bool
	restartAfterStop   bool // set by "restart" while the process is being stopped

	startTimer   *time.Timer
	stopTimer    *time.Timer
	backoffTimer *time.Timer
}

// exitInfo extracts the exit code and signal (if any) from a Wait error.
func exitInfo(err error) (code int, bySignal bool, sig syscall.Signal) {
	if err == nil {
		return 0, false, 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return -1, true, ws.Signal()
			}
			return ws.ExitStatus(), false, 0
		}
		return ee.ExitCode(), false, 0
	}
	return -1, false, 0
}

// exitDesc renders an exit result for logging.
func exitDesc(code int, bySignal bool, sig syscall.Signal) string {
	if bySignal {
		return fmt.Sprintf("killed by %s", config.SignalName(sig))
	}
	return fmt.Sprintf("exit code %d", code)
}

func expectedWord(expected bool) string {
	if expected {
		return "expected"
	}
	return "unexpected"
}

// backoffDelay returns how long to wait before the next start retry. It grows
// with the retry count but is capped so recovery stays responsive.
func backoffDelay(retries int) time.Duration {
	d := retries
	if d < 1 {
		d = 1
	}
	if d > 5 {
		d = 5
	}
	return time.Duration(d) * time.Second
}

// shouldRestart applies the autorestart policy to a finished RUNNING process.
func shouldRestart(p *config.Program, expected bool) bool {
	switch p.AutoRestart {
	case config.RestartAlways:
		return true
	case config.RestartNever:
		return false
	default: // unexpected
		return !expected
	}
}

// buildEnv returns the child environment: taskmaster's own environment with the
// program's configured variables appended (and thus overriding).
func buildEnv(env config.EnvMap) []string {
	out := os.Environ()
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// openStreams opens the stdout/stderr targets. An empty path means "discard",
// signaled by a nil file (the caller leaves the exec field nil, i.e. /dev/null).
func openStreams(p *config.Program) (out, errf *os.File, err error) {
	const flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if p.Stdout != "" {
		out, err = os.OpenFile(p.Stdout, flags, 0o644)
		if err != nil {
			return nil, nil, err
		}
	}
	if p.Stderr != "" {
		errf, err = os.OpenFile(p.Stderr, flags, 0o644)
		if err != nil {
			if out != nil {
				out.Close()
			}
			return nil, nil, err
		}
	}
	return out, errf, nil
}

// setUmask sets the process umask for the child about to be forked, returning
// the previous value so it can be restored. had is false when the program does
// not configure a umask (inherit).
func setUmask(u config.Umask) (old int, had bool) {
	if u < 0 {
		return 0, false
	}
	return syscall.Umask(int(u)), true
}

func closeFile(f *os.File) {
	if f != nil {
		f.Close()
	}
}
