-- 003_steam_id_nullable.sql — steam_id is unknown at link time.
--
-- Linking a Steam account stores only the username + encrypted password; the
-- account's SteamID64 isn't derivable from the login name. It is backfilled the
-- first time the worker logs in (the authenticated session reports its own
-- steam_id), so the column must allow NULL until then.

ALTER TABLE steam_accounts ALTER COLUMN steam_id DROP NOT NULL;
