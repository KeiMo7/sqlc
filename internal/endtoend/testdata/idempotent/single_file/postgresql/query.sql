-- name: GetWidget :one
SELECT * FROM widget WHERE id = $1;

-- name: ListWidgets :many
SELECT * FROM widget ORDER BY name;

-- name: CreateWidget :one
INSERT INTO widget (name, note) VALUES ($1, $2) RETURNING *;

-- name: DeleteWidget :exec
DELETE FROM widget WHERE id = $1;
