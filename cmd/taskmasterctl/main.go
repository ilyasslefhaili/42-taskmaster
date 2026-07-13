// Command taskmasterctl is a control client for the taskmaster daemon. It talks
// to the daemon's HTTP control API (the same one the web dashboard uses).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9001", "taskmaster daemon address")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	base := "http://" + *addr
	if err := run(base, args); err != nil {
		fmt.Fprintf(os.Stderr, "taskmasterctl: %v\n", err)
		os.Exit(1)
	}
}

func run(base string, args []string) error {
	switch cmd := args[0]; cmd {
	case "status":
		return status(base)
	case "start", "stop", "restart":
		if len(args) != 2 {
			return fmt.Errorf("usage: %s <name>", cmd)
		}
		return control(base, cmd, args[1])
	case "reload":
		return reload(base)
	default:
		return fmt.Errorf("unknown command %q (try: status, start, stop, restart, reload)", cmd)
	}
}

type statusView struct {
	Name          string `json:"name"`
	State         string `json:"state"`
	PID           int    `json:"pid"`
	UptimeSeconds int    `json:"uptime_seconds"`
	Retries       int    `json:"retries"`
	LastExit      int    `json:"last_exit"`
}

func status(base string) error {
	resp, err := http.Get(base + "/api/status")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var views []statusView
	if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tPID\tUPTIME\tRETRIES\tLAST-EXIT")
	for _, v := range views {
		pid, uptime := "-", "-"
		if v.PID != 0 {
			pid = fmt.Sprintf("%d", v.PID)
			uptime = (time.Duration(v.UptimeSeconds) * time.Second).String()
		}
		exit := "-"
		if v.LastExit >= 0 {
			exit = fmt.Sprintf("%d", v.LastExit)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", v.Name, v.State, pid, uptime, v.Retries, exit)
	}
	return w.Flush()
}

func control(base, action, name string) error {
	path := fmt.Sprintf("%s/api/programs/%s/%s", base, url.PathEscape(name), action)
	return post(path)
}

func reload(base string) error {
	return post(base + "/api/reload")
}

func post(rawURL string) error {
	resp, err := http.Post(rawURL, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var body map[string]string
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &body)

	if resp.StatusCode != http.StatusOK {
		if msg, ok := body["error"]; ok {
			return fmt.Errorf("%s", msg)
		}
		return fmt.Errorf("server returned %s", resp.Status)
	}
	if msg, ok := body["result"]; ok {
		fmt.Println(msg)
	}
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: taskmasterctl [-addr host:port] <command> [args]

commands:
  status              show the state of every program
  start <name>        start a program
  stop <name>         stop a program
  restart <name>      restart a program
  reload              reload the daemon's configuration
`)
}
