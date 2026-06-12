# Database Schema

Migrations in `control-plane/db/migrations/` as sequential numbered SQL files; they
are the source of truth and are applied transitively by `internal/testdb` in tests.

```sql
-- Users
CREATE TABLE users (
  id            UUID        PRIMARY KEY DEFAULT uuidv7(),
  username      TEXT        UNIQUE NOT NULL,
  password_hash TEXT        NOT NULL,        -- Argon2id
  kdf_salt      BYTEA       NOT NULL,        -- per-user salt for credential key derivation
  created_at    TIMESTAMPTZ DEFAULT now()
);

-- Steam accounts linked to users (one per user in V1)
CREATE TABLE steam_accounts (
  id             UUID  PRIMARY KEY DEFAULT uuidv7(),
  user_id        UUID  REFERENCES users(id) ON DELETE CASCADE,
  steam_id       TEXT,                       -- backfilled from worker's first login; null until then
  steam_username TEXT  NOT NULL,
  enc_password   BYTEA NOT NULL,             -- AES-256-GCM ciphertext
  enc_nonce      BYTEA NOT NULL,             -- GCM nonce
  sentry_hash    BYTEA,                      -- Steam Guard device trust; stored after first login
  created_at     TIMESTAMPTZ DEFAULT now()
);

-- Workers (V1: one row, inserted at startup)
CREATE TABLE workers (
  id         UUID PRIMARY KEY DEFAULT uuidv7(),
  state      TEXT NOT NULL DEFAULT 'STOPPED',
  last_seen  TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Spectator sessions
CREATE TABLE sessions (
  id               UUID   PRIMARY KEY DEFAULT uuidv7(),
  user_id          UUID   REFERENCES users(id),
  worker_id        UUID   REFERENCES workers(id),
  steam_account_id UUID   REFERENCES steam_accounts(id),
  target_steam_id  TEXT   NOT NULL,          -- Steam ID of friend being spectated
  match_id         BIGINT,                   -- resolved from GC; null until known
  state            TEXT   NOT NULL DEFAULT 'OFF',
  webrtc_url       TEXT,
  started_at       TIMESTAMPTZ,
  ended_at         TIMESTAMPTZ,
  created_at       TIMESTAMPTZ DEFAULT now()
);
```
