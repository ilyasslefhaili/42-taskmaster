// Package supervisor implements taskmaster's process-supervision engine. A
// single manager goroutine owns all process state and reacts to events
// (process exits, timers, and control commands) delivered over a channel, so
// state transitions are serialized and race-free.
package supervisor

import (
	"fmt"
	"log"
	"os/exec"
	"reflect"
	"sort"
	"sync"
	"syscall"
	"time"

	"42-taskmaster/internal/config"
)

// program groups the instances of a single configured program.
type program struct {
	cfg   *config.Program
	procs []*process
}

// Supervisor supervises every program described by a configuration.
type Supervisor struct {
	logger   *log.Logger
	programs map[string]*program
	order    []string // program names, sorted, for deterministic iteration

	events chan any

	shuttingDown  bool
	shutdownReply chan struct{}
	finished      bool

	wg sync.WaitGroup // tracks per-process monitor goroutines
}

// ProcStatus is a snapshot of one process for the "status" command.
type ProcStatus struct {
	Name     string
	State    string
	PID      int
	Uptime   time.Duration
	Retries  int
	LastExit int
}

// --- control events (carry reply channels for synchronous calls) ---

type evStart struct {
	name  string
	reply chan error
}
type evStop struct {
	name  string
	reply chan error
}
type evRestart struct {
	name  string
	reply chan error
}
type evStatus struct {
	reply chan []ProcStatus
}
type evNames struct {
	reply chan []string
}
type evReload struct {
	cfg   *config.Config
	reply chan struct{}
}
type evShutdown struct {
	reply chan struct{}
}

// --- internal events (posted by monitors and timers) ---

type evExited struct {
	p     *process
	runID int
	err   error
}
type evStartTimer struct {
	p     *process
	runID int
}
type evStopTimer struct {
	p     *process
	runID int
}
type evBackoff struct {
	p     *process
	runID int
}

// New builds a Supervisor for cfg. Call Start to begin supervision.
func New(cfg *config.Config, logger *log.Logger) *Supervisor {
	s := &Supervisor{
		logger:   logger,
		programs: make(map[string]*program),
		events:   make(chan any, 64),
	}
	for name := range cfg.Programs {
		s.order = append(s.order, name)
	}
	sort.Strings(s.order)

	for _, name := range s.order {
		s.programs[name] = buildProgram(name, cfg.Programs[name])
	}
	return s
}

// buildProgram creates a program and its (initially STOPPED) process instances.
func buildProgram(name string, pc *config.Program) *program {
	pr := &program{cfg: pc}
	for i := 0; i < pc.NumProcs; i++ {
		pn := name
		if pc.NumProcs > 1 {
			pn = fmt.Sprintf("%s:%d", name, i)
		}
		pr.procs = append(pr.procs, &process{
			name:         pn,
			prog:         pc,
			state:        Stopped,
			lastExitCode: -1,
		})
	}
	return pr
}

// Start launches the manager goroutine and autostarts programs.
func (s *Supervisor) Start() {
	go s.run()
}

func (s *Supervisor) run() {
	s.autostart()
	for !s.finished {
		s.dispatch(<-s.events)
		s.checkShutdownDone()
	}
}

func (s *Supervisor) autostart() {
	for _, name := range s.order {
		pr := s.programs[name]
		if !pr.cfg.AutoStart {
			continue
		}
		for _, p := range pr.procs {
			s.launch(p)
		}
	}
}

func (s *Supervisor) dispatch(e any) {
	switch ev := e.(type) {
	case evStart:
		ev.reply <- s.handleStart(ev.name)
	case evStop:
		ev.reply <- s.handleStop(ev.name)
	case evRestart:
		ev.reply <- s.handleRestart(ev.name)
	case evStatus:
		ev.reply <- s.snapshot()
	case evNames:
		ev.reply <- s.names()
	case evReload:
		s.handleReload(ev.cfg)
		ev.reply <- struct{}{}
	case evShutdown:
		s.handleShutdown(ev.reply)
	case evExited:
		s.handleExited(ev)
	case evStartTimer:
		s.handleStartTimer(ev)
	case evStopTimer:
		s.handleStopTimer(ev)
	case evBackoff:
		s.handleBackoff(ev)
	}
}

// --- launching and stopping ---

// launch spawns a fresh OS process for p and moves it to STARTING. It does not
// touch the retry counter; callers reset retries when appropriate.
func (s *Supervisor) launch(p *process) {
	argv, err := splitArgs(p.prog.Cmd)
	if err != nil || len(argv) == 0 {
		p.state = Fatal
		s.logf("%s: cannot parse command %q: %v", p.name, p.prog.Cmd, err)
		return
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = p.prog.WorkingDir
	cmd.Env = buildEnv(p.prog.Env)

	out, errf, oerr := openStreams(p.prog)
	if oerr != nil {
		p.state = Fatal
		s.logf("%s: cannot open output files: %v", p.name, oerr)
		return
	}
	if out != nil {
		cmd.Stdout = out
	}
	if errf != nil {
		cmd.Stderr = errf
	}

	old, had := setUmask(p.prog.Umask)
	startErr := cmd.Start()
	if had {
		syscall.Umask(old)
	}
	// The child has dup'd the fds; drop the parent's copies.
	closeFile(out)
	closeFile(errf)

	if startErr != nil {
		s.onStartFailure(p, fmt.Sprintf("%v", startErr))
		return
	}

	p.cmd = cmd
	p.pid = cmd.Process.Pid
	p.runID++
	p.state = Starting
	p.startedAt = time.Now()
	rid := p.runID

	p.startTimer = time.AfterFunc(time.Duration(p.prog.StartTime)*time.Second, func() {
		s.events <- evStartTimer{p: p, runID: rid}
	})

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		werr := cmd.Wait()
		s.events <- evExited{p: p, runID: rid, err: werr}
	}()

	s.logf("%s: started (pid %d)", p.name, p.pid)
}

// onStartFailure handles a process that could not be started or died before
// reaching starttime: retry with backoff, or give up as FATAL.
func (s *Supervisor) onStartFailure(p *process, reason string) {
	p.retries++
	if p.retries <= p.prog.StartRetries {
		p.state = Backoff
		delay := backoffDelay(p.retries)
		s.logf("%s: start failed (%s); retry %d/%d in %s",
			p.name, reason, p.retries, p.prog.StartRetries, delay)
		rid := p.runID
		p.backoffTimer = time.AfterFunc(delay, func() {
			s.events <- evBackoff{p: p, runID: rid}
		})
	} else {
		p.state = Fatal
		s.logf("%s: entered FATAL state (%s); gave up after %d retries",
			p.name, reason, p.prog.StartRetries)
	}
}

// beginStop sends the configured stop signal and arms the kill deadline.
func (s *Supervisor) beginStop(p *process) {
	sig, _ := p.prog.Signal()
	p.state = Stopping
	s.cancelStartTimer(p)
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(sig)
	}
	s.logf("%s: sending %s (pid %d)", p.name, p.prog.StopSignal, p.pid)

	rid := p.runID
	p.stopTimer = time.AfterFunc(time.Duration(p.prog.StopTime)*time.Second, func() {
		s.events <- evStopTimer{p: p, runID: rid}
	})
}

// stopProc stops p regardless of its current state (used by stop/shutdown).
func (s *Supervisor) stopProc(p *process) {
	switch p.state {
	case Running, Starting:
		p.restartAfterStop = false
		s.beginStop(p)
	case Backoff:
		s.cancelBackoff(p)
		p.state = Stopped
		s.logf("%s: stopped (canceled pending restart)", p.name)
	}
}

// --- control handlers ---

func (s *Supervisor) handleStart(name string) error {
	pr, ok := s.programs[name]
	if !ok {
		return fmt.Errorf("unknown program %q", name)
	}
	for _, p := range pr.procs {
		switch p.state {
		case Stopped, Exited, Fatal:
			p.retries = 0
			s.launch(p)
		case Backoff:
			s.cancelBackoff(p)
			p.retries = 0
			s.launch(p)
		}
	}
	return nil
}

func (s *Supervisor) handleStop(name string) error {
	pr, ok := s.programs[name]
	if !ok {
		return fmt.Errorf("unknown program %q", name)
	}
	for _, p := range pr.procs {
		s.stopProc(p)
	}
	return nil
}

func (s *Supervisor) handleRestart(name string) error {
	pr, ok := s.programs[name]
	if !ok {
		return fmt.Errorf("unknown program %q", name)
	}
	for _, p := range pr.procs {
		switch p.state {
		case Running, Starting:
			p.restartAfterStop = true
			s.beginStop(p)
		case Backoff:
			s.cancelBackoff(p)
			p.retries = 0
			s.launch(p)
		case Stopped, Exited, Fatal:
			p.retries = 0
			s.launch(p)
		}
	}
	return nil
}

func (s *Supervisor) handleShutdown(reply chan struct{}) {
	s.shuttingDown = true
	s.shutdownReply = reply
	for _, name := range s.order {
		for _, p := range s.programs[name].procs {
			s.stopProc(p)
		}
	}
}

// handleReload applies a new configuration to the running state. Programs whose
// spec is unchanged are left completely untouched (their processes keep
// running); removed programs are stopped and dropped; added or changed programs
// are (re)created and autostarted per the new spec.
func (s *Supervisor) handleReload(cfg *config.Config) {
	s.logf("reload: applying new configuration")

	// Removed programs: stop and drop.
	for _, name := range s.order {
		if _, ok := cfg.Programs[name]; !ok {
			s.logf("reload: removing program %q", name)
			for _, p := range s.programs[name].procs {
				s.stopProc(p)
			}
			delete(s.programs, name)
		}
	}

	// Added and changed programs.
	var newOrder []string
	for name := range cfg.Programs {
		newOrder = append(newOrder, name)
	}
	sort.Strings(newOrder)

	for _, name := range newOrder {
		newPc := cfg.Programs[name]
		old, exists := s.programs[name]
		switch {
		case !exists:
			s.logf("reload: adding program %q", name)
			s.addProgram(name, newPc)
		case reflect.DeepEqual(*old.cfg, *newPc):
			// Unchanged: do not disturb running processes.
		default:
			s.logf("reload: updating program %q", name)
			for _, p := range old.procs {
				s.stopProc(p) // detached below; will not restart
			}
			s.addProgram(name, newPc)
		}
	}

	s.order = newOrder
	s.logf("reload: complete (%d programs)", len(newOrder))
}

// addProgram installs a freshly built program and autostarts it if configured.
func (s *Supervisor) addProgram(name string, pc *config.Program) {
	pr := buildProgram(name, pc)
	s.programs[name] = pr
	if pc.AutoStart {
		for _, p := range pr.procs {
			s.launch(p)
		}
	}
}

func (s *Supervisor) names() []string {
	names := make([]string, len(s.order))
	copy(names, s.order)
	return names
}

// --- event handlers ---

func (s *Supervisor) handleExited(ev evExited) {
	p := ev.p
	if p.runID != ev.runID {
		return // stale; a newer run supersedes this one
	}
	s.cancelStartTimer(p)
	s.cancelStopTimer(p)

	code, bySignal, sig := exitInfo(ev.err)
	p.lastExitCode = code
	p.lastExitedBySignal = bySignal

	switch p.state {
	case Stopping:
		p.state = Stopped
		s.logf("%s: stopped (%s)", p.name, exitDesc(code, bySignal, sig))
		if p.restartAfterStop {
			p.restartAfterStop = false
			p.retries = 0
			s.launch(p)
		}
	case Starting:
		s.logf("%s: exited during startup (%s)", p.name, exitDesc(code, bySignal, sig))
		s.onStartFailure(p, exitDesc(code, bySignal, sig))
	case Running:
		expected := !bySignal && p.prog.ExitCodes.Contains(code)
		s.logf("%s: exited (%s, %s)", p.name, exitDesc(code, bySignal, sig), expectedWord(expected))
		if shouldRestart(p.prog, expected) {
			p.retries = 0
			s.launch(p)
		} else {
			p.state = Exited
		}
	default:
		p.state = Exited
	}
}

func (s *Supervisor) handleStartTimer(ev evStartTimer) {
	p := ev.p
	if p.runID != ev.runID || p.state != Starting {
		return
	}
	p.state = Running
	p.retries = 0
	s.logf("%s: running (pid %d, up %ds)", p.name, p.pid, p.prog.StartTime)
}

func (s *Supervisor) handleStopTimer(ev evStopTimer) {
	p := ev.p
	if p.runID != ev.runID || p.state != Stopping {
		return
	}
	s.logf("%s: did not exit within %ds, sending SIGKILL", p.name, p.prog.StopTime)
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

func (s *Supervisor) handleBackoff(ev evBackoff) {
	p := ev.p
	if p.runID != ev.runID || p.state != Backoff {
		return
	}
	s.launch(p)
}

// --- shutdown bookkeeping ---

func (s *Supervisor) checkShutdownDone() {
	if s.shuttingDown && !s.finished && s.activeCount() == 0 {
		close(s.shutdownReply)
		s.finished = true
	}
}

func (s *Supervisor) activeCount() int {
	n := 0
	for _, name := range s.order {
		for _, p := range s.programs[name].procs {
			switch p.state {
			case Starting, Running, Stopping, Backoff:
				n++
			}
		}
	}
	return n
}

// --- timer cancellation helpers ---

func (s *Supervisor) cancelStartTimer(p *process) {
	if p.startTimer != nil {
		p.startTimer.Stop()
		p.startTimer = nil
	}
}

func (s *Supervisor) cancelStopTimer(p *process) {
	if p.stopTimer != nil {
		p.stopTimer.Stop()
		p.stopTimer = nil
	}
}

func (s *Supervisor) cancelBackoff(p *process) {
	if p.backoffTimer != nil {
		p.backoffTimer.Stop()
		p.backoffTimer = nil
	}
}

// --- status ---

func (s *Supervisor) snapshot() []ProcStatus {
	var out []ProcStatus
	for _, name := range s.order {
		for _, p := range s.programs[name].procs {
			st := ProcStatus{
				Name:     p.name,
				State:    p.state.String(),
				Retries:  p.retries,
				LastExit: p.lastExitCode,
			}
			switch p.state {
			case Starting, Running, Stopping:
				st.PID = p.pid
				st.Uptime = time.Since(p.startedAt).Truncate(time.Second)
			}
			out = append(out, st)
		}
	}
	return out
}

func (s *Supervisor) logf(format string, a ...any) {
	s.logger.Printf(format, a...)
}

// --- public control API (safe to call from any goroutine) ---

// StartProgram starts every stopped instance of the named program.
func (s *Supervisor) StartProgram(name string) error {
	reply := make(chan error, 1)
	s.events <- evStart{name: name, reply: reply}
	return <-reply
}

// StopProgram gracefully stops every running instance of the named program.
func (s *Supervisor) StopProgram(name string) error {
	reply := make(chan error, 1)
	s.events <- evStop{name: name, reply: reply}
	return <-reply
}

// RestartProgram stops then starts every instance of the named program.
func (s *Supervisor) RestartProgram(name string) error {
	reply := make(chan error, 1)
	s.events <- evRestart{name: name, reply: reply}
	return <-reply
}

// ProgramNames returns the configured program names in sorted order.
func (s *Supervisor) ProgramNames() []string {
	reply := make(chan []string, 1)
	s.events <- evNames{reply: reply}
	return <-reply
}

// Reload applies a new configuration to the running state without disturbing
// programs whose spec is unchanged.
func (s *Supervisor) Reload(cfg *config.Config) {
	reply := make(chan struct{})
	s.events <- evReload{cfg: cfg, reply: reply}
	<-reply
}

// Status returns a snapshot of every process.
func (s *Supervisor) Status() []ProcStatus {
	reply := make(chan []ProcStatus, 1)
	s.events <- evStatus{reply: reply}
	return <-reply
}

// Shutdown gracefully stops all processes and waits for them to exit.
func (s *Supervisor) Shutdown() {
	reply := make(chan struct{})
	s.events <- evShutdown{reply: reply}
	<-reply
	s.wg.Wait()
}
