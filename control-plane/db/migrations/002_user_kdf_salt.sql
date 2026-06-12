-- 002_user_kdf_salt.sql — per-user salt for credential key derivation.
--
-- The AES-256 key that encrypts a user's Steam password is derived from the
-- user's login password with Argon2id (see internal/auth/crypto.go). That KDF
-- needs a stable per-user salt so the same login password always derives the
-- same key, letting the credential decrypt on a later login. The salt is not
-- secret; it only stops cross-user key reuse and precomputation.
--
-- Generated at registration. NOT NULL with no default: every user must have a
-- salt, and it must come from the application's CSPRNG, not the database.

ALTER TABLE users ADD COLUMN kdf_salt BYTEA NOT NULL;
