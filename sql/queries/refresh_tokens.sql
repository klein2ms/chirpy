-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (token, user_id)
VALUES ($1,
        $2)
RETURNING *;

-- name: GetRefreshToken :one
SELECT *
FROM refresh_tokens
WHERE token = $1;

-- name: RevokeRefreshToken :one
UPDATE refresh_tokens
SET revoked_at = NOW(),
    updated_at = NOW()
WHERE token = $1
RETURNING *;