-- name: GetReview :one
SELECT * FROM reviews WHERE id = $1;

-- name: ListReviewsForBook :many
SELECT * FROM reviews WHERE book_id = $1 ORDER BY rating DESC;

-- name: CreateReview :exec
INSERT INTO reviews (book_id, rating, body) VALUES ($1, $2, $3);
