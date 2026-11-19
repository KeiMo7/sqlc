-- name: TotalScore :one
SELECT sum(score) AS total FROM users;
