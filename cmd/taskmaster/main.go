package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"42-taskmaster/internal/api"
	"42-taskmaster/internal/config"
	"42-taskmaster/internal/shell"
	"42-taskmaster/internal/supervisor"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <config-file>\n", os.Args[0])
		os.Exit(1)
	}

	configPath := os.Args[1]
	cfg, err := config.Load(configPath)
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

	// reload re-reads the config file and applies it. On a parse error the
	// running configuration is kept unchanged.
	reload := func() error {
		newCfg, err := config.Load(configPath)
		if err != nil {
			logger.Printf("reload: failed, keeping current config: %v", err)
			return err
		}
		sv.Reload(newCfg)
		return nil
	}

	sh := shell.New(sv, reload)

	// Control HTTP server + web dashboard (client/server bonus). Runs in the
	// background so the control shell keeps the foreground.
	var httpSrv *http.Server
	if !strings.EqualFold(cfg.HTTPAddr, "off") {
		httpSrv = &http.Server{
			Addr:    cfg.HTTPAddr,
			Handler: api.New(sv, reload, logger).Handler(),
		}
		go func() {
			logger.Printf("http: listening on %s", cfg.HTTPAddr)
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Printf("http: server error: %v", err)
			}
		}()
		fmt.Printf("taskmaster: dashboard at http://%s\n", cfg.HTTPAddr)
	}

	// SIGHUP reloads the configuration while taskmaster keeps running.
	hupc := make(chan os.Signal, 1)
	signal.Notify(hupc, syscall.SIGHUP)
	go func() {
		for range hupc {
			logger.Printf("received SIGHUP, reloading configuration")
			_ = reload()
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM: restore the terminal, then stop.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigc
		fmt.Println("\ntaskmaster: shutting down...")
		logger.Printf("taskmaster: shutting down (signal)")
		closeHTTP(httpSrv)
		sh.Close()
		sv.Shutdown()
		os.Exit(0)
	}()

	sh.Run() // blocks until quit/exit or EOF

	fmt.Println("taskmaster: shutting down...")
	logger.Printf("taskmaster: shutting down (eof)")
	closeHTTP(httpSrv)
	sh.Close()
	sv.Shutdown()
}

func closeHTTP(srv *http.Server) {
	if srv != nil {
		srv.Close()
	}
}
