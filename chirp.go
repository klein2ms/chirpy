package main

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	database "github.com/klein2ms/chirpy/internal/database"
)

type CreateChirpRequest struct {
	Body   string    `json:"body"`
	UserId uuid.UUID `json:"user_id"`
}

func (req *CreateChirpRequest) IsValid() bool {
	return len(req.Body) <= 140
}

func (req *CreateChirpRequest) Sanitize() CreateChirpRequest {
	return CreateChirpRequest{
		Body:   contentFilter(req.Body),
		UserId: req.UserId,
	}
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

type CreateChirpResponse struct {
	Id        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	UserId    uuid.UUID `json:"user_id"`
}

func ToChirpResponse(chirp database.Chirp) CreateChirpResponse {
	return CreateChirpResponse{
		Id:        chirp.ID,
		CreatedAt: chirp.CreatedAt,
		UpdatedAt: chirp.UpdatedAt,
		Body:      chirp.Body,
		UserId:    chirp.UserID,
	}
}
