// Package api exposes the supervisor over HTTP: a small JSON control API plus
// an embedded web dashboard. It uses only the standard library. The same API
// backs both the browser dashboard and the taskmasterctl client.
package api

import (
	_ "embed"
	"encoding/json"
	"log"
	"net/http"

	"42-taskmaster/internal/supervisor"
)

//go:embed dashboard.html
var dashboardHTML []byte

// Server wires HTTP handlers to the supervisor.
type Server struct {
	sv     *supervisor.Supervisor
	reload func() error
	logger *log.Logger
}

// New returns an API server backed by sv. reload re-reads the configuration
// file (the same closure the shell and SIGHUP use).
func New(sv *supervisor.Supervisor, reload func() error, logger *log.Logger) *Server {
	return &Server{sv: sv, reload: reload, logger: logger}
}

// Handler returns the HTTP handler serving the API and dashboard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/programs/{name}/start", s.handleAction("start"))
	mux.HandleFunc("POST /api/programs/{name}/stop", s.handleAction("stop"))
	mux.HandleFunc("POST /api/programs/{name}/restart", s.handleAction("restart"))
	mux.HandleFunc("POST /api/reload", s.handleReload)
	mux.HandleFunc("GET /", s.handleIndex)
	return mux
}

// statusView is the JSON shape returned for each process.
type statusView struct {
	Name          string `json:"name"`
	State         string `json:"state"`
	PID           int    `json:"pid"`
	UptimeSeconds int    `json:"uptime_seconds"`
	Retries       int    `json:"retries"`
	LastExit      int    `json:"last_exit"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	snap := s.sv.Status()
	views := make([]statusView, 0, len(snap))
	for _, st := range snap {
		views = append(views, statusView{
			Name:          st.Name,
			State:         st.State,
			PID:           st.PID,
			UptimeSeconds: int(st.Uptime.Seconds()),
			Retries:       st.Retries,
			LastExit:      st.LastExit,
		})
	}
	writeJSON(w, http.StatusOK, views)
}

// handleAction returns a handler that runs a program control action.
func (s *Server) handleAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
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
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.logger.Printf("api: %s %q", action, name)
		writeJSON(w, http.StatusOK, map[string]string{"result": action + " ok"})
	}
}

func (s *Server) handleReload(w http.ResponseWriter, _ *http.Request) {
	if err := s.reload(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.logger.Printf("api: reload")
	writeJSON(w, http.StatusOK, map[string]string{"result": "reloaded"})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
