package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeJWT(t *testing.T) {
	t.Run("creates valid JWT", func(t *testing.T) {
		userID := uuid.New()
		secret := "test-secret"
		expiresIn := time.Hour

		token, err := MakeJWT(userID, secret, expiresIn)

		require.NoError(t, err)
		assert.NotEmpty(t, token)
	})

	t.Run("token contains correct claims", func(t *testing.T) {
		userID := uuid.New()
		secret := "test-secret"
		expiresIn := time.Hour

		token, err := MakeJWT(userID, secret, expiresIn)
		require.NoError(t, err)

		// Validate and check claims
		parsedID, err := ValidateJWT(token, secret)
		require.NoError(t, err)
		assert.Equal(t, userID, parsedID)
	})
}

func TestValidateJWT(t *testing.T) {
	secret := "test-secret"
	userID := uuid.New()

	t.Run("validates correct token", func(t *testing.T) {
		token, err := MakeJWT(userID, secret, time.Hour)
		require.NoError(t, err)

		parsedID, err := ValidateJWT(token, secret)
		require.NoError(t, err)
		assert.Equal(t, userID, parsedID)
	})

	t.Run("rejects expired token", func(t *testing.T) {
		token, err := MakeJWT(userID, secret, -time.Hour) // Already expired
		require.NoError(t, err)

		_, err = ValidateJWT(token, secret)
		assert.Error(t, err)
	})

	t.Run("rejects token with wrong secret", func(t *testing.T) {
		token, err := MakeJWT(userID, secret, time.Hour)
		require.NoError(t, err)

		_, err = ValidateJWT(token, "wrong-secret")
		assert.Error(t, err)
	})

	t.Run("rejects malformed token", func(t *testing.T) {
		_, err := ValidateJWT("not.a.token", secret)
		assert.Error(t, err)
	})

	t.Run("rejects empty token", func(t *testing.T) {
		_, err := ValidateJWT("", secret)
		assert.Error(t, err)
	})

	t.Run("rejects token with invalid UUID in subject", func(t *testing.T) {
		// Create a token manually with invalid UUID
		invalidToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
			Issuer:    "chirpy-access",
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(time.Hour)),
			Subject:   "not-a-uuid",
		})

		tokenString, err := invalidToken.SignedString([]byte(secret))
		require.NoError(t, err)

		_, err = ValidateJWT(tokenString, secret)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid user ID")
	})

	t.Run("token expires at correct time", func(t *testing.T) {
		expiresIn := 2 * time.Second
		token, err := MakeJWT(userID, secret, expiresIn)
		require.NoError(t, err)

		// Should be valid immediately
		_, err = ValidateJWT(token, secret)
		assert.NoError(t, err)

		// Wait for expiration
		time.Sleep(3 * time.Second)

		// Should now be expired
		_, err = ValidateJWT(token, secret)
		assert.Error(t, err)
	})
}

func TestJWTRoundTrip(t *testing.T) {
	t.Run("multiple users get different tokens", func(t *testing.T) {
		secret := "test-secret"
		user1 := uuid.New()
		user2 := uuid.New()

		token1, err := MakeJWT(user1, secret, time.Hour)
		require.NoError(t, err)

		token2, err := MakeJWT(user2, secret, time.Hour)
		require.NoError(t, err)

		assert.NotEqual(t, token1, token2)

		parsed1, err := ValidateJWT(token1, secret)
		require.NoError(t, err)
		assert.Equal(t, user1, parsed1)

		parsed2, err := ValidateJWT(token2, secret)
		require.NoError(t, err)
		assert.Equal(t, user2, parsed2)
	})
}
