-- name: GetUser :one
SELECT
  users.email,
  users.name,
  users.score,
  (SELECT profiles.bio FROM profiles WHERE profiles.user_id = users.id) AS profile_bio
FROM users
WHERE users.id = $1;

-- name: GetBio :one
SELECT profiles.bio
FROM profiles
JOIN users ON users.id = profiles.user_id
WHERE users.id = $1;

-- name: ListUserProfiles :many
SELECT users.email, profiles.bio
FROM users
LEFT JOIN profiles ON profiles.user_id = users.id;

-- name: UserStats :one
SELECT
  count(*) AS row_count,
  sum(users.score) AS total_score,
  coalesce(sum(users.score), 0) AS safe_total
FROM users;

-- name: UserView :many
SELECT name FROM user_names;
