// Package shell provides taskmaster's interactive control shell. When stdin is
// a terminal it uses readline for line editing, history, and tab completion;
// otherwise it falls back to plain line reading so the shell stays scriptable.
package shell

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/chzyer/readline"

	"42-taskmaster/internal/parser/shellparser"
	"42-taskmaster/internal/supervisor"
)

const prompt = "taskmaster> "

// Shell reads control commands and drives the supervisor.
type Shell struct {
	sv     *supervisor.Supervisor
	reload func() error
	out    io.Writer
	rl     *readline.Instance
}

// New returns a control shell for sv. reload is invoked by the "reload" command
// to re-read and apply the configuration file.
func New(sv *supervisor.Supervisor, reload func() error) *Shell {
	return &Shell{sv: sv, reload: reload, out: os.Stdout}
}

// Run reads and executes commands until "quit"/"exit" or EOF.
func (s *Shell) Run() {
	if isTerminal(os.Stdin) {
		s.runInteractive()
	} else {
		s.runPlain()
	}
}

// Close restores the terminal if readline is active. Safe to call more than once.
func (s *Shell) Close() {
	if s.rl != nil {
		s.rl.Close()
	}
}

func (s *Shell) runInteractive() {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          prompt,
		HistoryFile:     filepath.Join(os.TempDir(), ".taskmaster_history"),
		AutoComplete:    s.completer(),
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		// Fall back to plain mode if the terminal can't be set up.
		s.runPlain()
		return
	}
	s.rl = rl
	s.out = rl.Stdout()
	defer rl.Close()

	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			continue // Ctrl-C abandons the current line
		}
		if err == io.EOF {
			return
		}
		if s.dispatch(line) {
			return
		}
	}
}

func (s *Shell) runPlain() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Fprint(s.out, prompt)
	for scanner.Scan() {
		if s.dispatch(scanner.Text()) {
			return
		}
		fmt.Fprint(s.out, prompt)
	}
}

// completer offers command names, plus live program names for the commands that
// take one.
func (s *Shell) completer() *readline.PrefixCompleter {
	programs := readline.PcItemDynamic(func(string) []string {
		return s.sv.ProgramNames()
	})
	return readline.NewPrefixCompleter(
		readline.PcItem("help"),
		readline.PcItem("status"),
		readline.PcItem("start", programs),
		readline.PcItem("stop", programs),
		readline.PcItem("restart", programs),
		readline.PcItem("reload"),
		readline.PcItem("quit"),
		readline.PcItem("exit"),
	)
}

// dispatch parses and runs one line, returning true when the shell should exit.
func (s *Shell) dispatch(line string) (quit bool) {
	cmd, err := shellparser.Parse(line)
	if err != nil {
		fmt.Fprintf(s.out, "parse error: %v\n", err)
		return false
	}
	if cmd.Name == "" {
		return false
	}

	switch cmd.Name {
	case "help":
		s.printHelp()
	case "status":
		s.printStatus()
	case "start", "stop", "restart":
		s.control(cmd.Name, cmd.Args)
	case "reload":
		if err := s.reload(); err != nil {
			fmt.Fprintf(s.out, "reload failed: %v\n", err)
		} else {
			fmt.Fprintln(s.out, "configuration reloaded")
		}
	case "quit", "exit", "shutdown":
		return true
	default:
		fmt.Fprintf(s.out, "unknown command %q (try 'help')\n", cmd.Name)
	}
	return false
}

func (s *Shell) control(action string, args []string) {
	if len(args) != 1 {
		fmt.Fprintf(s.out, "usage: %s <name>\n", action)
		return
	}
	name := args[0]
	var err error
	switch action {
	case "start":
		err = s.sv.StartProgram(name)
	case "stop":
		err = s.sv.StopProgram(name)
	case "restart":
		err = s.sv.RestartProgram(name)
	}
	if err != nil {
		fmt.Fprintf(s.out, "error: %v\n", err)
	}
}

func (s *Shell) printHelp() {
	fmt.Fprintln(s.out, "commands:")
	fmt.Fprintln(s.out, "  status              show the state of every program")
	fmt.Fprintln(s.out, "  start <name>        start a program")
	fmt.Fprintln(s.out, "  stop <name>         stop a program")
	fmt.Fprintln(s.out, "  restart <name>      restart a program")
	fmt.Fprintln(s.out, "  reload              reload the configuration file")
	fmt.Fprintln(s.out, "  quit | exit         stop taskmaster and all programs")
}

func (s *Shell) printStatus() {
	w := tabwriter.NewWriter(s.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tPID\tUPTIME\tRETRIES\tLAST-EXIT")
	for _, st := range s.sv.Status() {
		pid, uptime := "-", "-"
		if st.PID != 0 {
			pid = fmt.Sprintf("%d", st.PID)
			uptime = st.Uptime.String()
		}
		exit := "-"
		if st.LastExit >= 0 {
			exit = fmt.Sprintf("%d", st.LastExit)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			st.Name, st.State, pid, uptime, st.Retries, exit)
	}
	w.Flush()
}

// isTerminal reports whether f is a character device (a terminal), using only
// the standard library.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
