package config

import (
	"fmt"
	"sort"
	"strings"
	"syscall"
)

// signalNames maps bare signal names (no "SIG" prefix) to their syscall value.
// This covers the signals a supervised program is plausibly asked to stop with.
var signalNames = map[string]syscall.Signal{
	"HUP":  syscall.SIGHUP,
	"INT":  syscall.SIGINT,
	"QUIT": syscall.SIGQUIT,
	"KILL": syscall.SIGKILL,
	"USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
	"TERM": syscall.SIGTERM,
	"STOP": syscall.SIGSTOP,
	"CONT": syscall.SIGCONT,
	"ABRT": syscall.SIGABRT,
	"ALRM": syscall.SIGALRM,
}

// ParseSignal resolves a signal name (case-insensitive, with or without a
// leading "SIG") to its syscall.Signal value.
func ParseSignal(name string) (syscall.Signal, error) {
	key := strings.ToUpper(strings.TrimSpace(name))
	key = strings.TrimPrefix(key, "SIG")
	sig, ok := signalNames[key]
	if !ok {
		return 0, fmt.Errorf("unknown signal %q (known: %s)", name, knownSignals())
	}
	return sig, nil
}

// knownSignals returns a sorted, comma-separated list of accepted signal names,
// used to make error messages actionable.
func knownSignals() string {
	names := make([]string, 0, len(signalNames))
	for n := range signalNames {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
