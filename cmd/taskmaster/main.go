package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"42-taskmaster/internal/config"
	"42-taskmaster/internal/supervisor"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <config-file>\n", os.Args[0])
		os.Exit(1)
	}

	cfg, err := config.Load(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "taskmaster: %v\n", err)
		os.Exit(1)
	}

	logFile, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taskmaster: cannot open log file %s: %v\n", cfg.LogFile, err)
		os.Exit(1)
	}
	defer logFile.Close()
	logger := log.New(logFile, "", log.LstdFlags)
	logger.Printf("taskmaster: started, supervising config %s", os.Args[1])

	fmt.Printf("taskmaster: logging events to %s (tail -f to follow)\n", cfg.LogFile)

	sv := supervisor.New(cfg, logger)
	sv.Start()

	// Graceful shutdown on Ctrl-C / SIGTERM.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		fmt.Println("\ntaskmaster: shutting down...")
		logger.Printf("taskmaster: shutting down (signal)")
		sv.Shutdown()
		os.Exit(0)
	}()

	commandLoop(sv)

	// Reached on EOF (Ctrl-D).
	fmt.Println("taskmaster: shutting down...")
	logger.Printf("taskmaster: shutting down (eof)")
	sv.Shutdown()
}

// commandLoop is a minimal control shell for phase 2. Phase 4 replaces it with
// a proper readline shell driven by the shellparser package.
func commandLoop(sv *supervisor.Supervisor) {
	fmt.Println("taskmaster: type 'help' for commands")
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("taskmaster> ")
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 {
			if quit := runCommand(sv, fields); quit {
				return
			}
		}
		fmt.Print("taskmaster> ")
	}
}

// runCommand executes one parsed command, returning true when the shell should
// exit.
func runCommand(sv *supervisor.Supervisor, fields []string) (quit bool) {
	cmd, args := fields[0], fields[1:]
	switch cmd {
	case "help":
		fmt.Println("commands: status | start <name> | stop <name> | restart <name> | quit")
	case "status":
		printStatus(sv)
	case "start", "stop", "restart":
		if len(args) != 1 {
			fmt.Printf("usage: %s <name>\n", cmd)
			return false
		}
		var err error
		switch cmd {
		case "start":
			err = sv.StartProgram(args[0])
		case "stop":
			err = sv.StopProgram(args[0])
		case "restart":
			err = sv.RestartProgram(args[0])
		}
		if err != nil {
			fmt.Printf("error: %v\n", err)
		}
	case "quit", "exit":
		return true
	default:
		fmt.Printf("unknown command %q (try 'help')\n", cmd)
	}
	return false
}

func printStatus(sv *supervisor.Supervisor) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tPID\tUPTIME\tRETRIES\tLAST-EXIT")
	for _, st := range sv.Status() {
		pid := "-"
		uptime := "-"
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
