-- 001_init.sql — initial schema.
-- Postgres 18+: uuidv7() is built in (time-ordered UUIDs, good index locality).

-- Users
CREATE TABLE users (
  id            UUID        PRIMARY KEY DEFAULT uuidv7(),
  username      TEXT        UNIQUE NOT NULL,
  password_hash TEXT        NOT NULL,        -- Argon2id
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Steam accounts linked to users (one per user in V1)
CREATE TABLE steam_accounts (
  id             UUID        PRIMARY KEY DEFAULT uuidv7(),
  user_id        UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  steam_id       TEXT        NOT NULL,
  steam_username TEXT        NOT NULL,
  enc_password   BYTEA       NOT NULL,       -- AES-256-GCM ciphertext
  enc_nonce      BYTEA       NOT NULL,       -- GCM nonce
  sentry_hash    BYTEA,                      -- Steam Guard device trust; stored after first login
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One Steam account per user in V1.
CREATE UNIQUE INDEX steam_accounts_user_id_key ON steam_accounts (user_id);

-- Workers (V1: one row, inserted at startup)
CREATE TABLE workers (
  id         UUID        PRIMARY KEY DEFAULT uuidv7(),
  state      TEXT        NOT NULL DEFAULT 'STOPPED',
  last_seen  TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Spectator sessions
CREATE TABLE sessions (
  id               UUID        PRIMARY KEY DEFAULT uuidv7(),
  user_id          UUID        NOT NULL REFERENCES users(id),
  worker_id        UUID        REFERENCES workers(id),
  steam_account_id UUID        REFERENCES steam_accounts(id),
  target_steam_id  TEXT        NOT NULL,     -- Steam ID of friend being spectated
  match_id         BIGINT,                   -- resolved from rich presence; null until known
  state            TEXT        NOT NULL DEFAULT 'OFF',
  webrtc_url       TEXT,
  started_at       TIMESTAMPTZ,
  ended_at         TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Look up the active session for a user / worker quickly.
CREATE INDEX sessions_user_id_idx   ON sessions (user_id);
CREATE INDEX sessions_worker_id_idx ON sessions (worker_id);
