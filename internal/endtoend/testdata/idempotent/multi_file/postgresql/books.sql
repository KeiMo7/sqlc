-- name: GetBook :one
SELECT * FROM books WHERE id = $1;

-- name: ListBooksByAuthor :many
SELECT * FROM books WHERE author_id = $1 ORDER BY title;

-- name: CreateBook :one
INSERT INTO books (author_id, title, published_at) VALUES ($1, $2, $3) RETURNING *;

-- name: DeleteBook :exec
DELETE FROM books WHERE id = $1;
