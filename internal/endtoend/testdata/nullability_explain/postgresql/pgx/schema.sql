CREATE TABLE users (
  id    BIGINT PRIMARY KEY,
  email TEXT,
  name  TEXT NOT NULL,
  score INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE profiles (
  user_id BIGINT NOT NULL,
  bio     TEXT NOT NULL
);

CREATE VIEW user_names AS
SELECT id, name FROM users;
