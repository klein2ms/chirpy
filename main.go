package main

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

type apiConfig struct {
	fileserverHits atomic.Int32
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (cfg *apiConfig) resetFileServerHits() {
	cfg.fileserverHits.Store(0)
}

func main() {
	cfg := apiConfig{}
	mux := http.NewServeMux()

	mux.Handle(
		"/app/",
		cfg.middlewareMetricsInc(http.StripPrefix("/app/", http.FileServer(http.Dir(".")))),
	)

	mux.HandleFunc(
		"GET /api/healthz",
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
		})

	mux.HandleFunc(
		"GET /api/metrics",
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf("Hits: %d", cfg.fileserverHits.Load())))
		})

	mux.HandleFunc(
		"POST /api/reset",
		func(w http.ResponseWriter, r *http.Request) {
			cfg.resetFileServerHits()
			w.WriteHeader(http.StatusOK)
		})

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	_ = server.ListenAndServe()
}
