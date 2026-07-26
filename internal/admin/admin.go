// Package admin is the per-role control surface: Prometheus metrics, health,
// and whatever role-specific handlers get registered (session restart, netem).
package admin

import (
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Server struct {
	mux *http.ServeMux
}

func New() *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.Handle("/metrics", promhttp.Handler())
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return s
}

func (s *Server) Handle(pattern string, h http.HandlerFunc) {
	s.mux.HandleFunc(pattern, h)
}

// Start serves in the background; admin availability is not worth crashing
// media paths over, so failures only log.
func (s *Server) Start(addr string) {
	go func() {
		if err := http.ListenAndServe(addr, s.mux); err != nil {
			log.Printf("admin server on %s: %v", addr, err)
		}
	}()
}
