package main

import (
	"fmt"
	"os"
	"sort"

	"42-taskmaster/internal/config"
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

	printSummary(cfg)
	// TODO(phase 2): start the supervisor engine.
}

// printSummary prints the fully parsed and defaulted configuration so the exact
// values the supervisor engine will consume can be eyeballed.
func printSummary(cfg *config.Config) {
	names := make([]string, 0, len(cfg.Programs))
	for name := range cfg.Programs {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Printf("loaded %d program(s):\n\n", len(names))
	for _, name := range names {
		p := cfg.Programs[name]
		sig, _ := p.Signal() // already validated during Load

		fmt.Printf("[%s]\n", name)
		fmt.Printf("  cmd          %q\n", p.Cmd)
		fmt.Printf("  numprocs     %d\n", p.NumProcs)
		fmt.Printf("  autostart    %t\n", p.AutoStart)
		fmt.Printf("  autorestart  %s\n", p.AutoRestart)
		fmt.Printf("  exitcodes    %v\n", []int(p.ExitCodes))
		fmt.Printf("  startretries %d\n", p.StartRetries)
		fmt.Printf("  starttime    %ds\n", p.StartTime)
		fmt.Printf("  stopsignal   %s (signal %d)\n", p.StopSignal, int(sig))
		fmt.Printf("  stoptime     %ds\n", p.StopTime)
		fmt.Printf("  umask        %s\n", umaskString(int(p.Umask)))
		fmt.Printf("  workingdir   %q\n", p.WorkingDir)
		fmt.Printf("  stdout       %s\n", streamString(p.Stdout))
		fmt.Printf("  stderr       %s\n", streamString(p.Stderr))
		fmt.Printf("  env          %s\n", envString(p.Env))
		fmt.Println()
	}
}

func umaskString(m int) string {
	if m < 0 {
		return "inherit"
	}
	return fmt.Sprintf("%04o", m)
}

func streamString(path string) string {
	if path == "" {
		return "(discard)"
	}
	return fmt.Sprintf("%q", path)
}

func envString(env config.EnvMap) string {
	if len(env) == 0 {
		return "(none)"
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%s=%s", k, env[k])
	}
	return out
}
