-- 004_refresh_token.sql — migrate Steam auth from sentry/password-at-rest to
-- refresh-token persistence (the modern IAuthenticationService model).
--
-- The worker no longer relies on a Steam Guard sentry to skip the guard, and the
-- Steam password is no longer stored: account link runs the QR (or credentials)
-- handshake once to obtain a long-lived refresh token. We persist only that
-- token, encrypted at rest under the user-derived key (same AES-256-GCM scheme
-- the password used), so a DB dump alone still can't authenticate. Cold logins
-- log onto the CM with the token; expiry/revocation → re-link.

ALTER TABLE steam_accounts
  DROP COLUMN sentry_hash,
  DROP COLUMN enc_password,
  DROP COLUMN enc_nonce,
  ADD COLUMN enc_refresh_token     BYTEA,        -- AES-256-GCM ciphertext; null until first link
  ADD COLUMN enc_refresh_nonce     BYTEA,        -- GCM nonce
  ADD COLUMN refresh_token_expires TIMESTAMPTZ;  -- from the token JWT `exp`, for proactive re-prompt

-- The account name is unknown for a QR link until the worker reports it from the
-- poll result, so it must allow NULL until backfilled.
ALTER TABLE steam_accounts ALTER COLUMN steam_username DROP NOT NULL;
