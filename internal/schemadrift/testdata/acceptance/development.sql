-- Development is the schema used for code generation.
CREATE TYPE account_state AS ENUM ('active', 'disabled', 'pending');

CREATE TABLE orgs (
  id bigint PRIMARY KEY
);

CREATE TABLE users (
  id bigint PRIMARY KEY,
  email text NOT NULL DEFAULT 'dev@example.com',
  org_id bigint REFERENCES orgs(id),
  state account_state NOT NULL
);

CREATE UNIQUE INDEX users_email_uq ON users (email);

CREATE TABLE audit_log (
  id bigint,
  created_at timestamp,
  actor text
);

CREATE VIEW active_users AS
  SELECT id, email FROM users WHERE state = 'active';

CREATE VIEW active_user_emails AS
  SELECT email FROM active_users;

CREATE TABLE events (
  id bigint,
  created_at date
) PARTITION BY RANGE (created_at);

CREATE TABLE events_2024 PARTITION OF events
  FOR VALUES FROM ('2024-01-01') TO ('2025-01-01');

CREATE TABLE legacy (
  a integer,
  b text
);
