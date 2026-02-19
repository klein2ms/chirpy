-- name: CreateChirp :one
INSERT INTO chirps (id, body, created_at, updated_at, user_id)
VALUES (gen_random_uuid(),
        $1,
        now(),
        now(),
        $2)
RETURNING *;

-- name: DeleteChirps :exec
DELETE
FROM chirps;

-- name: GetChirpsByCreatedAt :many
SELECT *
FROM chirps
ORDER BY created_at;

-- name: GetChirp :one
SELECT *
FROM chirps
WHERE id = $1;