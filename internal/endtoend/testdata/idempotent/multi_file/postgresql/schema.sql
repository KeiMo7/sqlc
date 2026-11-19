CREATE TABLE authors (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  bio TEXT
);

CREATE TABLE books (
  id BIGSERIAL PRIMARY KEY,
  author_id BIGINT NOT NULL REFERENCES authors(id),
  title TEXT NOT NULL,
  published_at TIMESTAMP
);

CREATE TABLE reviews (
  id BIGSERIAL PRIMARY KEY,
  book_id BIGINT NOT NULL REFERENCES books(id),
  rating INTEGER NOT NULL,
  body TEXT
);
