package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/klein2ms/chirpy/internal/auth"
	"github.com/klein2ms/chirpy/internal/database"
	_ "github.com/lib/pq"
)

type apiConfig struct {
	fileserverHits atomic.Int32
	dbQueries      *database.Queries
	jwtSecret      string
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

	_ = godotenv.Load()
	dbURL := os.Getenv("DB_URL")

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}

	cfg.dbQueries = database.New(db)
	cfg.jwtSecret = os.Getenv("JWT_SECRET")

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
			platform := os.Getenv("PLATFORM")
			if platform != "dev" {
				http.Error(w, "Forbidden", http.StatusForbidden)
			}

			err := cfg.dbQueries.DeleteUsers(r.Context())
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}

			w.WriteHeader(http.StatusOK)
		})

	mux.HandleFunc(
		"POST /api/users",
		func(w http.ResponseWriter, r *http.Request) {

			var createUserRequest CreateUserRequest
			err := json.NewDecoder(r.Body).Decode(&createUserRequest)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			defer func(Body io.ReadCloser) {
				_ = Body.Close()
			}(r.Body)

			hashedPassword, err := auth.HashPassword(createUserRequest.Password)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
			}

			user, err := cfg.dbQueries.CreateUser(
				r.Context(),
				database.CreateUserParams{
					Email:          createUserRequest.Email,
					HashedPassword: hashedPassword,
				})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			toReturn := CreateUserResponse{
				Id:        user.ID,
				Email:     user.Email,
				CreatedAt: user.CreatedAt,
				UpdatedAt: user.UpdatedAt,
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			err = json.NewEncoder(w).Encode(toReturn)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		})

	mux.HandleFunc(
		"POST /api/login",
		func(w http.ResponseWriter, r *http.Request) {

			var loginRequest LoginUserRequest

			err := json.NewDecoder(r.Body).Decode(&loginRequest)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			defer func(Body io.ReadCloser) {
				_ = Body.Close()
			}(r.Body)

			user, err := cfg.dbQueries.GetUserByEmail(r.Context(), loginRequest.Email)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			isAuthenticated, err := auth.CheckPasswordHash(loginRequest.Password, user.HashedPassword)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if !isAuthenticated {
				http.Error(w, "Incorrect email or password", http.StatusUnauthorized)
				return
			}

			var duration time.Duration

			if loginRequest.ExpiresInSeconds > 3600 || loginRequest.ExpiresInSeconds <= 0 {
				duration = 3600 * time.Second
			} else {
				duration = time.Duration(loginRequest.ExpiresInSeconds) * time.Second
			}

			token, err := auth.MakeJWT(
				user.ID,
				cfg.jwtSecret,
				duration)

			toReturn := LoginUserResponse{
				Id:        user.ID,
				Email:     user.Email,
				CreatedAt: user.CreatedAt,
				UpdatedAt: user.UpdatedAt,
				Token:     token,
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			err = json.NewEncoder(w).Encode(toReturn)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		})

	mux.HandleFunc(
		"POST /api/chirps",
		func(w http.ResponseWriter, r *http.Request) {

			var createChirpRequest CreateChirpRequest
			err := json.NewDecoder(r.Body).Decode(&createChirpRequest)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer func(Body io.ReadCloser) {
				_ = Body.Close()
			}(r.Body)

			token, err := auth.GetBearerToken(r.Header)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			user, err := auth.ValidateJWT(token, cfg.jwtSecret)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			createChirpRequest = createChirpRequest.Sanitize()

			if !createChirpRequest.IsValid() {
				http.Error(w, "Chirp is too long", http.StatusBadRequest)
				return
			}

			chirp, err := cfg.dbQueries.CreateChirp(
				r.Context(),
				database.CreateChirpParams{
					Body:   createChirpRequest.Body,
					UserID: user,
				},
			)

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			err = json.NewEncoder(w).Encode(ToChirpResponse(chirp))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

		})

	mux.HandleFunc(
		"GET /api/chirps",
		func(w http.ResponseWriter, r *http.Request) {
			chirps, err := cfg.dbQueries.GetChirpsByCreatedAt(r.Context())

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			var chirpsRes []CreateChirpResponse

			for _, chirp := range chirps {
				chirpsRes = append(chirpsRes, ToChirpResponse(chirp))
			}

			err = json.NewEncoder(w).Encode(chirpsRes)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		})

	mux.HandleFunc(
		"GET /api/chirps/{chirpID}",
		func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("chirpID")

			userId, err := uuid.Parse(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			chirp, err := cfg.dbQueries.GetChirp(r.Context(), userId)

			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					http.Error(w, "Not found", http.StatusNotFound)
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			err = json.NewEncoder(w).Encode(ToChirpResponse(chirp))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		})

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	_ = server.ListenAndServe()
}
