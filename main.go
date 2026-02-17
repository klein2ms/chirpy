package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
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
		"GET /admin/metrics",
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf(
				"<html>\n  <body>\n    <h1>Welcome, Chirpy Admin</h1>\n    <p>Chirpy has been visited %d times!</p>\n  </body>\n</html>", cfg.fileserverHits.Load())))
		})

	mux.HandleFunc(
		"POST /admin/reset",
		func(w http.ResponseWriter, r *http.Request) {
			cfg.resetFileServerHits()
			w.WriteHeader(http.StatusOK)
		})

	mux.HandleFunc(
		"POST /api/validate_chirp",
		func(w http.ResponseWriter, r *http.Request) {
			var body Body
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer func(Body io.ReadCloser) {
				_ = Body.Close()
			}(r.Body)

			var statusCode int
			var content string

			if len(body.Body) > 140 {
				fmt.Printf("Content-Length: %d\n", len(body.Body))
				statusCode = http.StatusBadRequest
				content = "{\"error\": \"Chirp is too long\"}"
			} else {
				content = fmt.Sprintf("{\"cleaned_body\": \"%s\"}", contentFilter(body.Body))
				statusCode = http.StatusOK
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			_, err = w.Write([]byte(content))
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("{\"error\": \"Something went wrong\"}"))
			}
		})

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	_ = server.ListenAndServe()
}

func contentFilter(content string) string {
	words := strings.Fields(content)

	badWords := []string{"kerfuffle", "sharbert", "fornax"}

	for i, word := range words {
		if slices.Contains(badWords, strings.ToLower(word)) {
			words[i] = "****"
		}
	}

	return strings.Join(words, " ")
}

type Body struct {
	Body string `json:"body"`
}
