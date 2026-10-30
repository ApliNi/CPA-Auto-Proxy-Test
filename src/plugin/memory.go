// In-memory observation statistics and bounded failure diagnostics.
// Replaces the disk-based SQLite database with zero-allocation in-memory structures,
// keeping the binary small and execution overhead minimal.
package plugin

import (
	"strings"
	"sync"
	"time"

	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/scorer"
)

// observationPoint records one outcome for sliding-window evaluation.
type observationPoint struct {
	ts int64
	ok bool
}

// maxRecentPointsPerProxy bounds the sliding window points kept in memory for
// each proxy of a credential.
const maxRecentPointsPerProxy = 100

// proxyMemoryStats stores the in-memory statistics for one proxy and credential.
type proxyMemoryStats struct {
	longSuccess int64
	longTotal   int64
	lastSeen    int64
	lastOK      bool
	recent      []observationPoint
}

func (s *proxyMemoryStats) record(ts int64, ok bool) {
	s.longTotal++
	if ok {
		s.longSuccess++
	}
	s.lastSeen = ts
	s.lastOK = ok
	s.recent = append(s.recent, observationPoint{ts: ts, ok: ok})
	if len(s.recent) > maxRecentPointsPerProxy {
		s.recent = s.recent[len(s.recent)-maxRecentPointsPerProxy:]
	}
}

func (s *proxyMemoryStats) windowStats(windowStart int64) scorer.Stats {
	var recentSuccess, recentTotal int64
	for _, pt := range s.recent {
		if pt.ts >= windowStart {
			recentTotal++
			if pt.ok {
				recentSuccess++
			}
		}
	}
	return scorer.Stats{
		RecentSuccess: recentSuccess,
		RecentTotal:   recentTotal,
		LongSuccess:   s.longSuccess,
		LongTotal:     s.longTotal,
	}
}

// globalProxyStats stores aggregated statistics and cooldown state for one proxy across all credentials.
type globalProxyStats struct {
	longSuccess         int64
	longTotal           int64
	consecutiveFailures int
	cooldownUntil       time.Time
}

// authMemoryState stores the in-memory state and proxy statistics for one credential.
type authMemoryState struct {
	authID              string
	currentProxy        string
	previousProxy       string
	updatedAt           time.Time
	lastSwitchAt        time.Time
	zeroSuccessFailures int
	consecutiveFailures map[string]int
	cooldownUntil       map[string]time.Time
	stats               map[string]*proxyMemoryStats
}

func newAuthMemoryState(authID string) *authMemoryState {
	return &authMemoryState{
		authID:              authID,
		consecutiveFailures: make(map[string]int),
		cooldownUntil:       make(map[string]time.Time),
		stats:               make(map[string]*proxyMemoryStats),
	}
}

func (a *authMemoryState) record(proxy string, ts int64, ok bool, baseCooldown time.Duration) {
	st, exists := a.stats[proxy]
	if !exists {
		st = &proxyMemoryStats{}
		a.stats[proxy] = st
	}
	st.record(ts, ok)

	if ok {
		a.consecutiveFailures[proxy] = 0
		delete(a.cooldownUntil, proxy)
		a.zeroSuccessFailures = 0
	} else {
		a.consecutiveFailures[proxy]++
		shift := a.consecutiveFailures[proxy] - 1
		if shift > 5 {
			shift = 5
		}
		if baseCooldown <= 0 {
			baseCooldown = 15 * time.Second
		}
		cd := baseCooldown * time.Duration(1<<shift)
		if cd > 10*time.Minute {
			cd = 10 * time.Minute
		}
		a.cooldownUntil[proxy] = time.Unix(ts, 0).Add(cd)
		a.zeroSuccessFailures++
	}
}

func (a *authMemoryState) cooldowns(now time.Time) map[string]time.Time {
	if a == nil {
		return nil
	}
	out := make(map[string]time.Time, len(a.cooldownUntil))
	for p, cd := range a.cooldownUntil {
		if now.Before(cd) {
			out[p] = cd
		}
	}
	return out
}

func (a *authMemoryState) isInSwitchDebounce(now time.Time, window time.Duration) bool {
	if a == nil || window <= 0 || a.lastSwitchAt.IsZero() {
		return false
	}
	return now.Sub(a.lastSwitchAt) < window
}

func (a *authMemoryState) isThrottledZombie(candidates []string, now time.Time) bool {
	if a == nil || len(candidates) == 0 {
		return false
	}
	var totalSuccess int64
	for _, st := range a.stats {
		totalSuccess += st.longSuccess
	}
	if totalSuccess > 0 {
		return false
	}
	for _, p := range candidates {
		st, ok := a.stats[p]
		if !ok || st.longTotal == 0 {
			return false
		}
	}
	if a.zeroSuccessFailures < len(candidates)*2 {
		return false
	}
	return !a.lastSwitchAt.IsZero() && now.Sub(a.lastSwitchAt) < 30*time.Second
}

func (a *authMemoryState) allStats(windowStart int64) map[string]scorer.Stats {
	if a == nil {
		return map[string]scorer.Stats{}
	}
	out := make(map[string]scorer.Stats, len(a.stats))
	for p, st := range a.stats {
		out[p] = st.windowStats(windowStart)
	}
	return out
}

func (a *authMemoryState) triedProxies() map[string]bool {
	if a == nil {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(a.stats))
	for p, st := range a.stats {
		if st.longTotal > 0 {
			out[p] = true
		}
	}
	return out
}

// FailureRecord captures one failed request diagnostic for operators to improve
// configuration or proxy lists in production.
type FailureRecord struct {
	TS           int64  `json:"ts"`
	AuthID       string `json:"auth_id"`
	Proxy        string `json:"proxy"`
	StatusCode   int    `json:"status_code"`
	ErrorSnippet string `json:"error_snippet,omitempty"`
	SwitchedTo   string `json:"switched_to,omitempty"`
}

const failureBufferCapacity = 100

// failureBuffer is a bounded ring buffer for recent upstream failure records.
type failureBuffer struct {
	mu      sync.Mutex
	records []FailureRecord
}

func newFailureBuffer() *failureBuffer {
	return &failureBuffer{records: make([]FailureRecord, 0, failureBufferCapacity)}
}

func (b *failureBuffer) Add(rec FailureRecord) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.records) >= failureBufferCapacity {
		copy(b.records, b.records[1:])
		b.records[len(b.records)-1] = rec
		return
	}
	b.records = append(b.records, rec)
}

func (b *failureBuffer) Snapshot(limit int) []FailureRecord {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.records)
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]FailureRecord, n)
	for i := range n {
		out[i] = b.records[len(b.records)-1-i]
	}
	return out
}

func truncateSnippet(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
