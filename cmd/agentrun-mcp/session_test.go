//go:build !windows

package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dmora/agentrun"
)

func TestSessionStore_CreateAndGet(t *testing.T) {
	store := newSessionStore(3)
	entry := &sessionEntry{
		id:           "mcp_aabbccdd11223344",
		backend:      backendClaude,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
	}
	if err := store.create(entry); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, ok := store.get(entry.id)
	if !ok {
		t.Fatal("get returned false for existing session")
	}
	if got.backend != backendClaude {
		t.Errorf("backend = %q, want %q", got.backend, backendClaude)
	}
}

func TestSessionStore_MaxCapacity(t *testing.T) {
	store := newSessionStore(2)
	for i := 0; i < 2; i++ {
		id, err := generateSessionID()
		if err != nil {
			t.Fatal(err)
		}
		if err := store.create(&sessionEntry{
			id:           id,
			backend:      backendClaude,
			createdAt:    time.Now(),
			lastActivity: time.Now(),
			proc:         &fakeProcess{output: make(chan agentrun.Message)},
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	// Third should fail.
	err := store.create(&sessionEntry{
		id:           "mcp_overflow",
		backend:      backendClaude,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
	})
	if err == nil {
		t.Fatal("expected error at capacity")
	}
}

func TestSessionStore_RateLimit(t *testing.T) {
	store := newSessionStore(100) // high cap so rate limit triggers first
	for i := 0; i < maxSessionCreationsPerMin; i++ {
		id, _ := generateSessionID()
		if err := store.create(&sessionEntry{
			id:           id,
			backend:      backendClaude,
			createdAt:    time.Now(),
			lastActivity: time.Now(),
			proc:         &fakeProcess{output: make(chan agentrun.Message)},
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	// Next create should hit rate limit.
	err := store.create(&sessionEntry{
		id:           "mcp_ratelimited",
		backend:      backendClaude,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
	})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
}

func TestSessionStore_Remove(t *testing.T) {
	store := newSessionStore(3)
	entry := &sessionEntry{
		id:           "mcp_toremove",
		backend:      backendClaude,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
	}
	store.create(entry)

	removed, ok := store.remove(entry.id)
	if !ok {
		t.Fatal("remove returned false")
	}
	if removed.id != entry.id {
		t.Errorf("removed.id = %q, want %q", removed.id, entry.id)
	}
	_, ok = store.get(entry.id)
	if ok {
		t.Fatal("get should return false after remove")
	}
}

func TestSessionStore_RemoveNonExistent(t *testing.T) {
	store := newSessionStore(3)
	_, ok := store.remove("mcp_nonexistent")
	if ok {
		t.Fatal("remove should return false for nonexistent ID")
	}
}

func TestSessionStore_ReapIdle(t *testing.T) {
	store := newSessionStore(3)
	old := &sessionEntry{
		id:           "mcp_old",
		backend:      backendClaude,
		createdAt:    time.Now().Add(-10 * time.Minute),
		lastActivity: time.Now().Add(-10 * time.Minute),
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
	}
	store.create(old)

	reaped := store.reapIdle(5 * time.Minute)
	if len(reaped) != 1 {
		t.Fatalf("reaped %d, want 1", len(reaped))
	}
	if reaped[0].id != old.id {
		t.Errorf("reaped[0].id = %q, want %q", reaped[0].id, old.id)
	}
	_, ok := store.get(old.id)
	if ok {
		t.Fatal("reaped session should be removed from store")
	}
}

func TestSessionStore_ReapIdle_ActiveNotReaped(t *testing.T) {
	store := newSessionStore(3)
	active := &sessionEntry{
		id:           "mcp_active",
		backend:      backendClaude,
		createdAt:    time.Now().Add(-10 * time.Minute),
		lastActivity: time.Now().Add(-10 * time.Minute),
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
		activeTurns:  1, // has active turn
	}
	store.create(active)

	reaped := store.reapIdle(5 * time.Minute)
	if len(reaped) != 0 {
		t.Fatalf("reaped %d, want 0 (active session should not be reaped)", len(reaped))
	}
}

func TestSessionStore_ReapIdle_RecentlyActiveNotReaped(t *testing.T) {
	store := newSessionStore(3)
	recent := &sessionEntry{
		id:           "mcp_recent",
		backend:      backendClaude,
		createdAt:    time.Now().Add(-1 * time.Minute),
		lastActivity: time.Now(), // just active
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
	}
	store.create(recent)

	reaped := store.reapIdle(5 * time.Minute)
	if len(reaped) != 0 {
		t.Fatalf("reaped %d, want 0 (recently active session should not be reaped)", len(reaped))
	}
}

func TestSessionStore_List(t *testing.T) {
	store := newSessionStore(3)
	now := time.Now()
	entry := &sessionEntry{
		id:           "mcp_aabbccdd11223344aabbccdd11223344",
		backend:      backendClaude,
		createdAt:    now,
		lastActivity: now,
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
	}
	store.create(entry)

	infos := store.list()
	if len(infos) != 1 {
		t.Fatalf("list() returned %d, want 1", len(infos))
	}
	if infos[0].IDPrefix != "mcp_aabbccdd…" {
		t.Errorf("IDPrefix = %q, want %q", infos[0].IDPrefix, "mcp_aabbccdd…")
	}
	if infos[0].Backend != backendClaude {
		t.Errorf("Backend = %q, want %q", infos[0].Backend, backendClaude)
	}
}

func TestSessionStore_StopAll_Concurrent(t *testing.T) {
	store := newSessionStore(3)
	var procs []*fakeProcess
	for i := 0; i < 3; i++ {
		id, _ := generateSessionID()
		p := &fakeProcess{output: make(chan agentrun.Message)}
		procs = append(procs, p)
		store.create(&sessionEntry{
			id:           id,
			backend:      backendClaude,
			createdAt:    time.Now(),
			lastActivity: time.Now(),
			proc:         p,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store.stopAll(ctx)

	for i, p := range procs {
		if p.stopCount() == 0 {
			t.Errorf("process %d: Stop not called", i)
		}
	}
	// Store should be empty.
	if infos := store.list(); len(infos) != 0 {
		t.Errorf("list() after stopAll = %d, want 0", len(infos))
	}
}

func TestSessionStore_ConcurrentOps(t *testing.T) {
	store := newSessionStore(50)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _ := generateSessionID()
			entry := &sessionEntry{
				id:           id,
				backend:      backendClaude,
				createdAt:    time.Now(),
				lastActivity: time.Now(),
				proc:         &fakeProcess{output: make(chan agentrun.Message)},
			}
			_ = store.create(entry)
			store.get(id)
			store.list()
			store.remove(id)
		}()
	}
	wg.Wait()
}

func TestGenerateSessionID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := generateSessionID()
		if err != nil {
			t.Fatalf("generateSessionID: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
}

func TestGenerateSessionID_Format(t *testing.T) {
	id, err := generateSessionID()
	if err != nil {
		t.Fatalf("generateSessionID: %v", err)
	}
	// "mcp_" + 32 hex chars = 36 total.
	if len(id) != 36 {
		t.Errorf("len = %d, want 36", len(id))
	}
	if id[:4] != "mcp_" {
		t.Errorf("prefix = %q, want %q", id[:4], "mcp_")
	}
	// All chars after prefix should be hex.
	for _, c := range id[4:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex char %q in ID %q", string(c), id)
		}
	}
}

func TestMaskID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"mcp_aabbccdd11223344", "mcp_aabbccdd…"},
		{"short", "short"},
		{"exactly12ch", "exactly12ch"},
		{"thirteen_char", "thirteen_cha…"},
	}
	for _, tt := range tests {
		got := maskID(tt.input)
		if got != tt.want {
			t.Errorf("maskID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
