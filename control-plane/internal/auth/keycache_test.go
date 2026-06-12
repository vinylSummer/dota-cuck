package auth

import (
	"bytes"
	"testing"
	"time"
)

func TestKeyCachePutGet(t *testing.T) {
	c := NewKeyCache(time.Hour)
	key := []byte("0123456789abcdef0123456789abcdef")
	c.Put("user-1", key)

	got, ok := c.Get("user-1")
	if !ok {
		t.Fatal("expected cached key")
	}
	if !bytes.Equal(got, key) {
		t.Fatalf("got %x, want %x", got, key)
	}
}

func TestKeyCacheGetMissing(t *testing.T) {
	c := NewKeyCache(time.Hour)
	if _, ok := c.Get("nobody"); ok {
		t.Fatal("expected miss for unknown user")
	}
}

func TestKeyCacheDeleteEvicts(t *testing.T) {
	c := NewKeyCache(time.Hour)
	c.Put("user-1", []byte("k"))
	c.Delete("user-1")
	if _, ok := c.Get("user-1"); ok {
		t.Fatal("key not evicted after Delete")
	}
}

func TestKeyCacheExpiry(t *testing.T) {
	c := NewKeyCache(time.Hour)
	base := time.Now()
	c.now = func() time.Time { return base }
	c.Put("user-1", []byte("k"))

	// Just before expiry: still present.
	c.now = func() time.Time { return base.Add(time.Hour - time.Second) }
	if _, ok := c.Get("user-1"); !ok {
		t.Fatal("key expired too early")
	}

	// At/after expiry: gone.
	c.now = func() time.Time { return base.Add(time.Hour) }
	if _, ok := c.Get("user-1"); ok {
		t.Fatal("expired key still returned")
	}
}
