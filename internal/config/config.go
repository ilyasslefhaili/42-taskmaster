// Package config loads, defaults, and validates the taskmaster configuration
// file. The on-disk format is YAML; see config.yaml at the repo root for an
// example. Every supervised program is described under the top-level
// "programs" mapping, keyed by program name.
package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

// Restart policy values for a program's autorestart field.
const (
	RestartAlways     = "always"
	RestartNever      = "never"
	RestartUnexpected = "unexpected"
)

// DefaultLogFile is where lifecycle events are written when the config does not
// specify a logfile.
const DefaultLogFile = "taskmaster.log"

// DefaultHTTPAddr is the address the control HTTP server (and web dashboard)
// listens on when the config does not specify one.
const DefaultHTTPAddr = "127.0.0.1:9001"

// Config is the fully parsed and validated configuration.
type Config struct {
	// LogFile is the local file lifecycle events are logged to.
	LogFile string `yaml:"logfile"`
	// HTTPAddr is the listen address for the control API and web dashboard.
	// Set it to "off" to disable the HTTP server.
	HTTPAddr string              `yaml:"httpaddr"`
	Programs map[string]*Program `yaml:"programs"`
}

// Program is the supervision spec for a single program. Every field the subject
// requires is represented here. Fields left out of the YAML take the defaults
// applied in defaultProgram.
type Program struct {
	Name         string    `yaml:"-"` // filled from the map key, not the YAML body
	Cmd          string    `yaml:"cmd"`
	NumProcs     int       `yaml:"numprocs"`
	Umask        Umask     `yaml:"umask"`
	WorkingDir   string    `yaml:"workingdir"`
	AutoStart    bool      `yaml:"autostart"`
	AutoRestart  string    `yaml:"autorestart"`
	ExitCodes    ExitCodes `yaml:"exitcodes"`
	StartRetries int       `yaml:"startretries"`
	StartTime    int       `yaml:"starttime"` // seconds up before "started"
	StopSignal   string    `yaml:"stopsignal"`
	StopTime     int       `yaml:"stoptime"` // seconds to wait before SIGKILL
	Stdout       string    `yaml:"stdout"`   // path, or "" to discard
	Stderr       string    `yaml:"stderr"`   // path, or "" to discard
	Env          EnvMap    `yaml:"env"`
}

// Umask is a Unix file-creation mask. It is unmarshalled from an octal literal
// (e.g. 022) to match the subject's example. A value of -1 means "inherit".
type Umask int

// UnmarshalYAML parses the umask as an octal number regardless of any leading
// zero, since 022 must mean octal 022, not decimal 22.
func (u *Umask) UnmarshalYAML(value *yaml.Node) error {
	n, err := strconv.ParseInt(strings.TrimSpace(value.Value), 8, 32)
	if err != nil {
		return fmt.Errorf("invalid umask %q: must be an octal number", value.Value)
	}
	*u = Umask(n)
	return nil
}

// ExitCodes is the set of exit statuses considered an "expected" exit. It
// accepts either a single scalar (exitcodes: 0) or a list (exitcodes: [0, 2]).
type ExitCodes []int

// UnmarshalYAML accepts both the scalar and sequence spellings.
func (e *ExitCodes) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var n int
		if err := value.Decode(&n); err != nil {
			return fmt.Errorf("invalid exitcodes %q: %w", value.Value, err)
		}
		*e = ExitCodes{n}
		return nil
	}
	var list []int
	if err := value.Decode(&list); err != nil {
		return fmt.Errorf("invalid exitcodes list: %w", err)
	}
	*e = list
	return nil
}

// Contains reports whether code is one of the expected exit codes.
func (e ExitCodes) Contains(code int) bool {
	for _, c := range e {
		if c == code {
			return true
		}
	}
	return false
}

// EnvMap is a set of environment variables. Values are kept as their literal
// source text so that numeric-looking values (ANSWER: 42) stay strings.
type EnvMap map[string]string

// UnmarshalYAML reads each value's raw scalar text, avoiding type coercion.
func (m *EnvMap) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("env must be a mapping of name to value")
	}
	out := make(map[string]string, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content); i += 2 {
		out[value.Content[i].Value] = value.Content[i+1].Value
	}
	*m = out
	return nil
}

// Signal resolves the program's configured stop signal.
func (p *Program) Signal() (syscall.Signal, error) {
	return ParseSignal(p.StopSignal)
}

// defaultProgram returns a Program pre-populated with taskmaster's defaults.
// These mirror supervisor's defaults where one exists.
func defaultProgram() Program {
	return Program{
		NumProcs:     1,
		Umask:        -1, // inherit
		AutoStart:    true,
		AutoRestart:  RestartUnexpected,
		ExitCodes:    ExitCodes{0},
		StartRetries: 3,
		StartTime:    1,
		StopSignal:   "TERM",
		StopTime:     10,
	}
}

// UnmarshalYAML decodes a program on top of the defaults, so keys absent from
// the YAML keep their default value instead of Go's zero value.
func (p *Program) UnmarshalYAML(value *yaml.Node) error {
	type raw Program // strip the method set to avoid infinite recursion
	d := raw(defaultProgram())
	if err := value.Decode(&d); err != nil {
		return err
	}
	*p = Program(d)
	return nil
}

// Load reads, defaults, and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if len(cfg.Programs) == 0 {
		return nil, fmt.Errorf("config defines no programs")
	}
	if cfg.LogFile == "" {
		cfg.LogFile = DefaultLogFile
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = DefaultHTTPAddr
	}
	for name, p := range cfg.Programs {
		p.Name = name
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks every program for internal consistency. Programs are checked
// in name order so errors are reported deterministically.
func (c *Config) Validate() error {
	for _, name := range c.sortedNames() {
		if err := c.Programs[name].validate(); err != nil {
			return fmt.Errorf("program %q: %w", name, err)
		}
	}
	return nil
}

func (c *Config) sortedNames() []string {
	names := make([]string, 0, len(c.Programs))
	for name := range c.Programs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (p *Program) validate() error {
	if strings.TrimSpace(p.Cmd) == "" {
		return fmt.Errorf("cmd must not be empty")
	}
	if p.NumProcs < 1 {
		return fmt.Errorf("numprocs must be >= 1, got %d", p.NumProcs)
	}
	switch p.AutoRestart {
	case RestartAlways, RestartNever, RestartUnexpected:
	default:
		return fmt.Errorf("autorestart must be one of always/never/unexpected, got %q", p.AutoRestart)
	}
	for _, code := range p.ExitCodes {
		if code < 0 || code > 255 {
			return fmt.Errorf("exit code %d out of range 0-255", code)
		}
	}
	if p.StartRetries < 0 {
		return fmt.Errorf("startretries must be >= 0, got %d", p.StartRetries)
	}
	if p.StartTime < 0 {
		return fmt.Errorf("starttime must be >= 0, got %d", p.StartTime)
	}
	if p.StopTime < 0 {
		return fmt.Errorf("stoptime must be >= 0, got %d", p.StopTime)
	}
	if _, err := p.Signal(); err != nil {
		return err
	}
	return nil
}
