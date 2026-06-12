package auth

import (
	"sync"
	"time"
)

// KeyCache holds per-user credential-encryption keys in memory. The login
// password — the only input that can derive the key — is present only at login,
// so the key is derived once there and cached for the session's lifetime to
// encrypt/decrypt Steam credentials on later JWT-authed requests.
//
// Keys are never written to disk. Entries are evicted explicitly on logout
// (Delete) and lazily once they pass the TTL (which should match the JWT TTL),
// so a key never outlives the token it was minted for.
type KeyCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time // injectable for tests
	entries map[string]cacheEntry
}

type cacheEntry struct {
	key     []byte
	expires time.Time
}

// NewKeyCache returns a cache whose entries live for ttl.
func NewKeyCache(ttl time.Duration) *KeyCache {
	return &KeyCache{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]cacheEntry),
	}
}

// Put stores key for userID, (re)setting its expiry to now+ttl.
func (c *KeyCache) Put(userID string, key []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[userID] = cacheEntry{key: key, expires: c.now().Add(c.ttl)}
}

// Get returns the cached key for userID. It returns false if there is no entry
// or the entry has expired (in which case it is evicted).
func (c *KeyCache) Get(userID string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[userID]
	if !ok {
		return nil, false
	}
	if !c.now().Before(e.expires) {
		delete(c.entries, userID)
		return nil, false
	}
	return e.key, true
}

// Delete evicts userID's key (called on logout).
func (c *KeyCache) Delete(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, userID)
}
