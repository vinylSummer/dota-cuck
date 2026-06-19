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
  id                    UUID        PRIMARY KEY DEFAULT uuidv7(),
  user_id               UUID        REFERENCES users(id) ON DELETE CASCADE,
  steam_id              TEXT,        -- backfilled when the link handshake completes; null until then
  steam_username        TEXT,        -- null until backfilled (unknown up front for a QR link)
  enc_refresh_token     BYTEA,       -- AES-256-GCM ciphertext of the Steam refresh token; null until linked
  enc_refresh_nonce     BYTEA,       -- GCM nonce
  refresh_token_expires TIMESTAMPTZ, -- from the token JWT `exp`, for proactive re-prompt
  created_at            TIMESTAMPTZ DEFAULT now()
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
  match_id         BIGINT,                   -- resolved from rich presence; null until known
  state            TEXT   NOT NULL DEFAULT 'OFF',
  webrtc_url       TEXT,
  started_at       TIMESTAMPTZ,
  ended_at         TIMESTAMPTZ,
  created_at       TIMESTAMPTZ DEFAULT now()
);
```
