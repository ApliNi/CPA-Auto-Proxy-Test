// Package plugin implements the cpa-auto-proxy-test usage hook: it observes failed
// requests for the configured credential type, records per-proxy outcomes,
// and routes credentials to the best available proxy by rewriting the
// proxy_url member of the credential file.
package plugin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"

	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/authfile"
	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/config"
	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/matcher"
	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/scorer"
)

// AuthEntry is the credential snapshot the engine needs from the host.
type AuthEntry struct {
	// ID identifies the credential record.
	ID string
	// Index is the stable runtime credential index.
	Index string
	// Type is the credential provider type.
	Type string
	// Path is the backing auth file path when available.
	Path string
	// RuntimeOnly reports whether the credential has no backing auth file.
	RuntimeOnly bool
}

// HostAPI is the host surface the engine depends on. main.go bridges it to
// the C ABI host callbacks.
type HostAPI interface {
	// AuthList returns the current credential snapshot.
	AuthList() ([]AuthEntry, error)
	// Log forwards a structured log entry to the host.
	Log(level string, message string, fields map[string]any)
}

// Engine wires config, matcher, memory statistics, scorer, and credential-file writes
// into the usage hook.
type Engine struct {
	mu           sync.Mutex
	cfg          config.Config
	match        *matcher.Matcher
	host         HostAPI
	auths        map[string]*authMemoryState
	failures     *failureBuffer
	cleanup      time.Time
	authList     []AuthEntry
	authListedAt time.Time
	events       *eventBuffer
	stop         chan struct{}
	stopped      sync.Once
}

// New creates the in-memory engine, runs the initial cleanup, and
// starts the cleanup timer. The caller owns the engine lifetime; Close stops
// the timer and releases resources.
func New(cfg config.Config, host HostAPI) (*Engine, error) {
	match, errCompile := matcher.New(cfg.FailurePatterns)
	if errCompile != nil {
		return nil, errCompile
	}
	engine := &Engine{
		cfg:      cfg,
		match:    match,
		host:     host,
		auths:    make(map[string]*authMemoryState),
		failures: newFailureBuffer(),
		events:   newEventBuffer(),
		stop:     make(chan struct{}),
	}
	// Initial cleanup and sync: scan credentials, purge obsolete proxies
	// not in the active configuration, and warm up memory state.
	if errCleanup := engine.Cleanup(context.Background()); errCleanup != nil {
		engine.host.Log("warn", "cpa-auto-proxy-test: initial cleanup failed: "+errCleanup.Error(), nil)
	}
	engine.mu.Lock()
	engine.cleanupAtLocked(time.Now())
	engine.mu.Unlock()
	go engine.runCleanupLoop()
	return engine, nil
}

// Reconfigure applies new configuration, purges proxies removed from the
// configuration across all physical auth files, and resets the cleanup timer.
func (e *Engine) Reconfigure(cfg config.Config) error {
	match, errCompile := matcher.New(cfg.FailurePatterns)
	if errCompile != nil {
		return errCompile
	}
	e.mu.Lock()
	e.cfg = cfg
	e.match = match
	e.cleanupAtLocked(time.Now())
	e.mu.Unlock()
	_ = e.Cleanup(context.Background())
	return nil
}

// Close stops the cleanup timer.
func (e *Engine) Close() error {
	e.stopped.Do(func() {
		close(e.stop)
	})
	return nil
}

// Config returns the active configuration snapshot.
func (e *Engine) Config() config.Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

// HandleUsage processes one usage record from the host.
//
// Records for unmanaged credential types are ignored. A success records one
// success observation for the currently applied proxy; a failure matching a
// configured pattern records one failure observation and triggers the proxy
// switch. Failures that match no pattern are ignored.
//
// When the credential still runs on the global default (no plugin proxy
// applied yet), a success observation also applies the best configured proxy
// directly so proxy selection takes effect.
func (e *Engine) HandleUsage(record pluginapi.UsageRecord) {
	e.mu.Lock()
	cfg := e.cfg
	failMatch := e.match
	e.mu.Unlock()

	// CPA 的 UsageRecord.AuthType 是凭证形态(oauth/apikey),
	// 提供方类型(antigravity)只在 Provider 字段(executor Identifier, 小写)。
	if !cfg.MatchesAuthType(record.Provider) {
		return
	}
	if record.Failed && !failMatch.Match(record.Failure.StatusCode, record.Failure.Body) {
		// 未匹配配置规则的失败不视为代理故障信号
		return
	}
	ok := !record.Failed

	authID, _, path, hasAuthInfo := e.resolveAuthInfo(record)
	if authID == "" {
		e.AddEvent("warn", "usage", record.AuthID,
			"usage record for provider "+record.Provider+" has no resolvable auth info (auth_id empty, index "+record.AuthIndex+")")
		return
	}

	now := time.Now()

	e.mu.Lock()
	st, exists := e.auths[authID]
	if !exists {
		st = newAuthMemoryState(authID)
		e.auths[authID] = st
	}
	if st.currentProxy == "" && path != "" {
		if proxyURL, errRead := authfile.ReadProxyURL(path); errRead == nil && proxyURL != "" {
			st.currentProxy = proxyURL
		}
	}

	current := st.currentProxy
	observed := current
	if observed == "" {
		observed = scorer.DefaultProxy
	}
	st.record(observed, now.Unix(), ok)

	shouldSwitch := !ok
	if ok && current == "" && hasAuthInfo && len(cfg.Proxies) > 0 {
		// 全局默认状态下首次成功：立刻分配并绑定最佳代理
		shouldSwitch = true
	}

	if !shouldSwitch {
		e.mu.Unlock()
		return
	}

	selected, errSwitch := e.switchProxyLocked(authID, path, st, cfg)
	e.mu.Unlock()

	if errSwitch != nil {
		e.host.Log("error", "cpa-auto-proxy-test: switch proxy failed: "+errSwitch.Error(), map[string]any{"auth_id": authID})
		e.AddEvent("error", "switch", authID, "switch proxy failed: "+errSwitch.Error())
	}
	if !ok {
		e.failures.Add(FailureRecord{
			TS:           now.Unix(),
			AuthID:       authID,
			Proxy:        observed,
			StatusCode:   record.Failure.StatusCode,
			ErrorSnippet: truncateSnippet(record.Failure.Body, 512),
			SwitchedTo:   selected,
		})
	}
}

// resolveAuthInfo maps a usage record to the credential identifier, runtime
// index, and backing file path. Records without AuthID fall back to the host
// runtime entry resolved through AuthIndex.
func (e *Engine) resolveAuthInfo(record pluginapi.UsageRecord) (authID string, authIndex string, path string, hasAuthInfo bool) {
	authID = strings.TrimSpace(record.AuthID)
	authIndex = strings.TrimSpace(record.AuthIndex)
	if authID != "" && authIndex != "" {
		entryPath, ok := e.authPath(authIndex)
		if ok {
			path = entryPath
			hasAuthInfo = true
		}
		return authID, authIndex, path, hasAuthInfo
	}
	if authIndex == "" {
		return "", "", "", false
	}
	entry, ok := e.authEntry(authIndex)
	if !ok {
		return "", authIndex, "", false
	}
	if entry.ID == "" {
		return "", authIndex, entry.Path, entry.Path != ""
	}
	return entry.ID, authIndex, entry.Path, entry.Path != ""
}

// switchProxyLocked picks the best proxy according to historical scores,
// writes it to the physical auth file, and updates memory state.
// Must be called with e.mu held.
func (e *Engine) switchProxyLocked(authID string, path string, st *authMemoryState, cfg config.Config) (string, error) {
	if len(cfg.Proxies) == 0 {
		return "", nil
	}
	windowStart := time.Now().Add(-time.Duration(cfg.WindowHours) * time.Hour).Unix()
	stats := st.allStats(windowStart)
	tried := st.triedProxies()
	current := st.currentProxy
	if current == "" {
		current = scorer.DefaultProxy
	}

	selected := scorer.Select(cfg.Proxies, untriedSet(cfg.Proxies, tried), current, stats)
	if selected == "" || selected == current {
		return current, nil
	}

	if path != "" {
		if errApply := authfile.ApplyProxy(path, selected); errApply != nil {
			return "", errApply
		}
	}

	st.currentProxy = selected
	st.updatedAt = time.Now()

	e.host.Log("info", fmt.Sprintf("cpa-auto-proxy-test: switched %s to %s", authID, selected), map[string]any{
		"auth_id": authID,
		"proxy":   selected,
		"from":    current,
	})
	e.events.Add(Event{
		TS:      time.Now().Unix(),
		Level:   "info",
		Kind:    "switch",
		AuthID:  authID,
		Message: fmt.Sprintf("switched proxy from %s to %s", current, selected),
	})
	return selected, nil
}

func untriedSet(candidates []string, tried map[string]bool) map[string]bool {
	untried := make(map[string]bool, len(candidates))
	for _, proxy := range candidates {
		untried[proxy] = !tried[proxy]
	}
	return untried
}

// Cleanup removes memory records for credentials that no longer exist,
// and scans all physical auth files to purge obsolete proxies not in config.Proxies.
func (e *Engine) Cleanup(ctx context.Context) error {
	entries, errList := e.host.AuthList()
	if errList != nil {
		return errList
	}
	existing := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.ID != "" {
			existing[entry.ID] = true
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	removedAuths := 0
	for authID := range e.auths {
		if !existing[authID] {
			delete(e.auths, authID)
			removedAuths++
		}
	}
	e.syncAndCleanProxiesLocked(entries)

	if removedAuths > 0 {
		e.events.Add(Event{
			TS:      time.Now().Unix(),
			Level:   "info",
			Kind:    "cleanup",
			Message: fmt.Sprintf("cleanup removed %d stale credential records from memory", removedAuths),
		})
	}
	return nil
}

// syncAndCleanProxiesLocked inspects all managed physical auth files:
// if an auth file has a proxy_url that is NOT in config.Proxies, it is physically
// cleared from the file to prevent broken/stale proxies from harming requests.
// Must be called with e.mu held.
func (e *Engine) syncAndCleanProxiesLocked(entries []AuthEntry) {
	configuredProxies := make(map[string]bool, len(e.cfg.Proxies))
	for _, p := range e.cfg.Proxies {
		configuredProxies[p] = true
	}

	for _, entry := range entries {
		if entry.Path == "" || entry.ID == "" {
			continue
		}
		if !e.cfg.MatchesAuthType(entry.Type) {
			continue
		}

		proxyURL, errRead := authfile.ReadProxyURL(entry.Path)
		if errRead != nil {
			continue
		}

		st, exists := e.auths[entry.ID]
		if !exists {
			st = newAuthMemoryState(entry.ID)
			e.auths[entry.ID] = st
		}

		if proxyURL != "" {
			if !configuredProxies[proxyURL] {
				// 发现不存在于配置中的废弃/坏代理：物理清除！
				if errClear := authfile.ClearProxy(entry.Path); errClear != nil {
					e.host.Log("warn", fmt.Sprintf("cpa-auto-proxy-test: failed to clear obsolete proxy %s from %s: %s", proxyURL, entry.ID, errClear.Error()), nil)
				} else {
					e.events.Add(Event{
						TS:      time.Now().Unix(),
						Level:   "warn",
						Kind:    "clean",
						AuthID:  entry.ID,
						Message: fmt.Sprintf("cleared obsolete proxy %s not present in active configuration", proxyURL),
					})
				}
				st.currentProxy = ""
			} else {
				st.currentProxy = proxyURL
			}
		} else {
			st.currentProxy = ""
		}
	}
}

const cleanupInterval = 10 * time.Minute

// authSnapshotTTL bounds how long the engine reuses one host.auth.list
// snapshot, keeping request completions off the credential-list RPC hot path.
const authSnapshotTTL = 30 * time.Second

func (e *Engine) cleanupAtLocked(now time.Time) {
	e.cleanup = now.Add(cleanupInterval)
}

func (e *Engine) runCleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-e.stop:
			return
		case now := <-ticker.C:
			e.mu.Lock()
			due := !now.Before(e.cleanup)
			e.mu.Unlock()
			if !due {
				continue
			}
			if errCleanup := e.Cleanup(context.Background()); errCleanup != nil {
				e.host.Log("warn", "cpa-auto-proxy-test: cleanup failed: "+errCleanup.Error(), nil)
			} else {
				e.mu.Lock()
				e.cleanupAtLocked(now)
				e.mu.Unlock()
			}
		}
	}
}

func (e *Engine) authEntry(authIndex string) (AuthEntry, bool) {
	entries := e.authEntries()
	for _, entry := range entries {
		if entry.Index == authIndex {
			return entry, true
		}
	}
	return AuthEntry{}, false
}

// authEntries returns the host credential snapshot, cached briefly so a
// burst of usage records does not turn into one credential-list RPC per
// request. Cleanup always queries the host directly for freshness.
func (e *Engine) authEntries() []AuthEntry {
	e.mu.Lock()
	if !time.Now().After(e.authListedAt.Add(authSnapshotTTL)) && e.authList != nil {
		entries := e.authList
		e.mu.Unlock()
		return entries
	}
	e.mu.Unlock()
	entries, errList := e.host.AuthList()
	if errList != nil {
		e.host.Log("warn", "cpa-auto-proxy-test: list auth files failed: "+errList.Error(), nil)
		return nil
	}
	e.mu.Lock()
	e.authList = entries
	e.authListedAt = time.Now()
	e.mu.Unlock()
	return entries
}

func (e *Engine) authPath(authIndex string) (string, bool) {
	if entry, ok := e.authEntry(authIndex); ok {
		return entry.Path, entry.Path != ""
	}
	return "", false
}

// AuthStats returns the scorer stats for an authID at windowStart (unix seconds).
func (e *Engine) AuthStats(authID string, windowStart int64) map[string]scorer.Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, exists := e.auths[authID]
	if !exists {
		return map[string]scorer.Stats{}
	}
	return st.allStats(windowStart)
}

// AuthState returns the in-memory state proxy for an authID.
func (e *Engine) AuthState(authID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, exists := e.auths[authID]
	if !exists {
		return ""
	}
	return st.currentProxy
}

// RecordObservation records an observation directly into memory.
func (e *Engine) RecordObservation(authID, proxy string, ts int64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, exists := e.auths[authID]
	if !exists {
		st = newAuthMemoryState(authID)
		e.auths[authID] = st
	}
	st.record(proxy, ts, ok)
}

// SetAuthState sets the in-memory currentProxy for an authID.
func (e *Engine) SetAuthState(authID, proxy string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, exists := e.auths[authID]
	if !exists {
		st = newAuthMemoryState(authID)
		e.auths[authID] = st
	}
	st.currentProxy = proxy
}
