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
	polkaApiKey    string
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
	cfg.polkaApiKey = os.Getenv("POLKA_KEY")

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
				Id:          user.ID,
				Email:       user.Email,
				CreatedAt:   user.CreatedAt,
				UpdatedAt:   user.UpdatedAt,
				IsChirpyRed: user.IsChirpyRed,
			}

			writeJson(w, http.StatusCreated, toReturn)
		})

	mux.HandleFunc(
		"PUT /api/users",
		func(w http.ResponseWriter, r *http.Request) {
			id, err := authenticateUser(r, cfg.jwtSecret)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			var updateUserRequest UpdateUserRequest
			err = json.NewDecoder(r.Body).Decode(&updateUserRequest)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			defer func(Body io.ReadCloser) {
				_ = Body.Close()
			}(r.Body)

			passwordHash, err := auth.HashPassword(updateUserRequest.Password)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			user, err := cfg.dbQueries.UpdateUser(r.Context(), database.UpdateUserParams{
				ID:             id,
				Email:          updateUserRequest.Email,
				HashedPassword: passwordHash,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			toReturn := UpdateUserResponse{
				Id:          user.ID,
				Email:       user.Email,
				CreatedAt:   user.CreatedAt,
				UpdatedAt:   user.UpdatedAt,
				IsChirpyRed: user.IsChirpyRed,
			}

			writeJson(w, http.StatusOK, toReturn)
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

			duration := 3600 * time.Second

			token, err := auth.MakeJWT(
				user.ID,
				cfg.jwtSecret,
				duration)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			refreshToken, err := auth.MakeRefreshToken()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			_, err = cfg.dbQueries.CreateRefreshToken(r.Context(), database.CreateRefreshTokenParams{
				Token:  refreshToken,
				UserID: user.ID,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			toReturn := LoginUserResponse{
				Id:           user.ID,
				Email:        user.Email,
				CreatedAt:    user.CreatedAt,
				UpdatedAt:    user.UpdatedAt,
				IsChirpyRed:  user.IsChirpyRed,
				Token:        token,
				RefreshToken: refreshToken,
			}

			writeJson(w, http.StatusOK, toReturn)
		})

	mux.HandleFunc(
		"POST /api/refresh",
		func(w http.ResponseWriter, r *http.Request) {

			bearerToken, err := auth.GetBearerToken(r.Header)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			refreshToken, err := cfg.dbQueries.GetRefreshToken(r.Context(), bearerToken)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			if refreshToken.RevokedAt.Valid {
				http.Error(w, "Refresh token is revoked", http.StatusUnauthorized)
				return
			}

			if refreshToken.ExpiresAt.Before(time.Now().UTC()) {
				http.Error(w, "Refresh token is expired", http.StatusUnauthorized)
				return
			}

			duration := 3600 * time.Second

			token, err := auth.MakeJWT(
				refreshToken.UserID,
				cfg.jwtSecret,
				duration)

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			toReturn := RefreshTokenResponse{
				AuthToken: token,
			}

			writeJson(w, http.StatusOK, toReturn)
		})

	mux.HandleFunc(
		"POST /api/revoke",
		func(w http.ResponseWriter, r *http.Request) {
			bearerToken, err := auth.GetBearerToken(r.Header)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			_, err = cfg.dbQueries.RevokeRefreshToken(r.Context(), bearerToken)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusNoContent)
			return
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

			user, err := authenticateUser(r, cfg.jwtSecret)
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

			writeJson(w, http.StatusCreated, ToChirpResponse(chirp))
		})

	mux.HandleFunc(
		"GET /api/chirps",
		func(w http.ResponseWriter, r *http.Request) {
			authorId := r.URL.Query().Get("author_id")

			var chirps []database.Chirp

			if authorId != "" {
				userId, err := uuid.Parse(authorId)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				chirps, err = cfg.dbQueries.GetChirpsByUserId(r.Context(), userId)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

			} else {
				chirps, err = cfg.dbQueries.GetChirpsByCreatedAt(r.Context())

				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}

			var chirpsRes []CreateChirpResponse

			for _, chirp := range chirps {
				chirpsRes = append(chirpsRes, ToChirpResponse(chirp))
			}

			writeJson(w, http.StatusOK, chirpsRes)
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

	mux.HandleFunc(
		"DELETE /api/chirps/{chirpID}",
		func(w http.ResponseWriter, r *http.Request) {
			userId, err := authenticateUser(r, cfg.jwtSecret)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			id := r.PathValue("chirpID")

			chirpId, err := uuid.Parse(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			chirp, err := cfg.dbQueries.GetChirp(r.Context(), chirpId)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					http.Error(w, "Not found", http.StatusNotFound)
					return
				}

				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if chirp.UserID != userId {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}

			err = cfg.dbQueries.DeleteChirp(r.Context(), chirpId)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusNoContent)
			return
		})

	mux.HandleFunc(
		"POST /api/polka/webhooks",
		func(w http.ResponseWriter, r *http.Request) {
			apiKey, err := auth.GetAPIKey(r.Header)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}

			if apiKey != cfg.polkaApiKey {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}

			var userUpgradedEventRequest UserUpgradedEventRequest

			err = json.NewDecoder(r.Body).Decode(&userUpgradedEventRequest)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			switch userUpgradedEventRequest.Event {
			case "user.upgraded":
				userId, err := uuid.Parse(userUpgradedEventRequest.Data.UserId)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				_, err = cfg.dbQueries.UpgradeToChirpyRed(r.Context(), userId)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						http.Error(w, "Not found", http.StatusNotFound)
						return
					}
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			default:
				w.WriteHeader(http.StatusNoContent)
				return
			}

		})

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	_ = server.ListenAndServe()
}

func authenticateUser(r *http.Request, jwtSecret string) (uuid.UUID, error) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		return uuid.Nil, err
	}

	user, err := auth.ValidateJWT(token, jwtSecret)
	if err != nil {
		return uuid.Nil, err
	}

	return user, nil
}

func writeJson[T any](w http.ResponseWriter, statusCode int, v T) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	err := json.NewEncoder(w).Encode(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
