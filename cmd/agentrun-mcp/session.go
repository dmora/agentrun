//go:build !windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dmora/agentrun"
)

const (
	maxLiveSessions           = 3
	sessionIdleTime           = 5 * time.Minute
	reaperInterval            = 30 * time.Second
	maxSessionCreationsPerMin = 10
	maxMessageBytes           = 1 << 20 // 1 MiB
)

// sessionEntry holds per-session state.
type sessionEntry struct {
	id           string
	proc         agentrun.Process
	backend      string
	stderrW      *cappedWriter
	createdAt    time.Time
	lastActivity time.Time
	activeTurns  int32      // atomic
	mu           sync.Mutex // serializes Send calls within this session
}

// sessionInfo is a metadata snapshot returned by list().
type sessionInfo struct {
	IDPrefix     string  `json:"id_prefix"`
	Backend      string  `json:"backend"`
	CreatedAt    string  `json:"created_at"`
	LastActivity string  `json:"last_activity"`
	IdleSeconds  float64 `json:"idle_seconds"`
}

// sessionStore is a mutex-protected map of live sessions.
type sessionStore struct {
	mu                 sync.Mutex
	sessions           map[string]*sessionEntry
	maxSessions        int
	creationTimestamps []time.Time
}

func newSessionStore(maxSessions int) *sessionStore {
	return &sessionStore{
		sessions:    make(map[string]*sessionEntry),
		maxSessions: maxSessions,
	}
}

// create atomically checks capacity + rate limit and inserts entry.
func (s *sessionStore) create(entry *sessionEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.sessions) >= s.maxSessions {
		return fmt.Errorf("session limit reached (%d)", s.maxSessions)
	}

	// Rate limit: prune timestamps older than 1 minute, then check count.
	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)
	valid := s.creationTimestamps[:0]
	for _, ts := range s.creationTimestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	s.creationTimestamps = valid

	if len(s.creationTimestamps) >= maxSessionCreationsPerMin {
		return fmt.Errorf("session creation rate limit exceeded (%d per minute)", maxSessionCreationsPerMin)
	}

	s.sessions[entry.id] = entry
	s.creationTimestamps = append(s.creationTimestamps, now)
	return nil
}

// get returns the session entry or false.
func (s *sessionStore) get(id string) (*sessionEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[id]
	return e, ok
}

// remove removes and returns the entry, or returns false.
func (s *sessionStore) remove(id string) (*sessionEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[id]
	if ok {
		delete(s.sessions, id)
	}
	return e, ok
}

// reapIdle removes idle entries with no active turns and returns them.
func (s *sessionStore) reapIdle(maxIdle time.Duration) []*sessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var reaped []*sessionEntry
	for id, e := range s.sessions {
		if atomic.LoadInt32(&e.activeTurns) > 0 {
			continue
		}
		if now.Sub(e.lastActivity) > maxIdle {
			delete(s.sessions, id)
			reaped = append(reaped, e)
		}
	}
	return reaped
}

// list returns a metadata snapshot with truncated IDs.
func (s *sessionStore) list() []sessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	infos := make([]sessionInfo, 0, len(s.sessions))
	for _, e := range s.sessions {
		infos = append(infos, sessionInfo{
			IDPrefix:     maskID(e.id),
			Backend:      e.backend,
			CreatedAt:    e.createdAt.Format(time.RFC3339),
			LastActivity: e.lastActivity.Format(time.RFC3339),
			IdleSeconds:  now.Sub(e.lastActivity).Seconds(),
		})
	}
	return infos
}

// stopAll stops all sessions concurrently, honoring the caller's context.
func (s *sessionStore) stopAll(ctx context.Context) {
	s.mu.Lock()
	entries := make([]*sessionEntry, 0, len(s.sessions))
	for _, e := range s.sessions {
		entries = append(entries, e)
	}
	s.sessions = make(map[string]*sessionEntry)
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Add(1)
		go func(entry *sessionEntry) {
			defer wg.Done()
			stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_ = entry.proc.Stop(stopCtx)
		}(e)
	}
	wg.Wait()
}

// generateSessionID returns "mcp_" + 32 hex chars from crypto/rand.
func generateSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return "mcp_" + hex.EncodeToString(b), nil
}

// maskID returns a truncated ID prefix for logging and list output.
func maskID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

// startReaper runs a ticker-based goroutine that reaps idle sessions.
func startReaper(ctx context.Context, store *sessionStore, maxIdle, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				reaped := store.reapIdle(maxIdle)
				for _, e := range reaped {
					slog.Info("reaped idle session", "session", maskID(e.id), "backend", e.backend)
					stopProcess(e.proc)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}
