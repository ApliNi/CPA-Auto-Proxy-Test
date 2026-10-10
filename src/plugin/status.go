// Status snapshot construction for the Management API status page and the
// /data and /export endpoints. The snapshot reads credential entries, the
// in-memory observation state, and the event ring buffer without mutating
// engine state, so it is safe to serve while HandleUsage runs concurrently.
package plugin

import (
	"context"
	"strings"
	"time"

	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/config"
	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/scorer"
)

// PluginName and PluginVersion identify the plugin in export archives.
const (
	PluginName    = "CPA-Auto-Proxy-Test"
	PluginVersion = "0.1.0"
)

// StatusAccount is one credential with its per-proxy observation statistics.
type StatusAccount struct {
	AuthID       string             `json:"auth_id"`
	AuthIndex    string             `json:"auth_index"`
	Type         string             `json:"type"`
	Path         string             `json:"path"`
	CurrentProxy string             `json:"current_proxy"`
	AppliedProxy string             `json:"applied_proxy"`
	StateProxy   string             `json:"state_proxy"`
	TotalSuccess int64              `json:"total_success"`
	TotalFailed  int64              `json:"total_failed"`
	Observations []ProxyObservation `json:"observations"`
}

// ProxyObservation holds the scorer statistics for one proxy and credential.
type ProxyObservation struct {
	Proxy         string  `json:"proxy"`
	RecentSuccess int64   `json:"recent_success"`
	RecentTotal   int64   `json:"recent_total"`
	LongSuccess   int64   `json:"long_success"`
	LongTotal     int64   `json:"long_total"`
	Score         float64 `json:"score"`
	// LastSeen is the newest observation timestamp (unix seconds) for this
	// proxy and credential; 0 means no observation exists.
	LastSeen int64 `json:"last_seen"`
	// LastOK is the outcome of the newest observation; it is only
	// meaningful when LastSeen is non-zero.
	LastOK bool `json:"last_ok"`
}

// StatusSnapshot is the full state surfaced by the /data endpoint and
// embedded in the status page.
type StatusSnapshot struct {
	GeneratedAt string          `json:"generated_at"`
	Config      StatusConfig    `json:"config"`
	Accounts    []StatusAccount `json:"accounts"`
	Events      []Event         `json:"events"`
}

// StatusConfig summarizes the active configuration for display.
type StatusConfig struct {
	AuthType             string   `json:"auth_type"`
	Proxies              []string `json:"proxies"`
	WindowHours          int      `json:"window_hours"`
	FailurePatternsCount int      `json:"failure_patterns_count"`
}

// ExportDocument is the compact production diagnostic archive produced by GET /export.
// It contains configuration, account scores, bounded recent upstream failures, and
// runtime events for operators to improve settings or proxy candidate lists.
type ExportDocument struct {
	ExportedAt     string          `json:"exported_at"`
	Plugin         ExportPlugin    `json:"plugin"`
	Config         ExportConfig    `json:"config"`
	Accounts       []StatusAccount `json:"accounts"`
	RecentFailures []FailureRecord `json:"recent_failures"`
	Events         []Event         `json:"events"`
}

// ExportPlugin identifies the exporting plugin.
type ExportPlugin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ExportConfig extends the status config summary with the raw failure
// patterns so archives can be re-analyzed without the original config file.
type ExportConfig struct {
	StatusConfig
	FailurePatterns []string `json:"failure_patterns"`
}

// StatusSnapshot builds the status snapshot.
func (e *Engine) StatusSnapshot(ctx context.Context) (*StatusSnapshot, error) {
	e.mu.Lock()
	cfg := e.cfg
	e.mu.Unlock()

	windowStart := time.Now().Add(-time.Duration(cfg.WindowHours) * time.Hour).Unix()
	accounts := e.statusAccounts(cfg, windowStart)
	return &StatusSnapshot{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Config:      newStatusConfig(cfg),
		Accounts:    accounts,
		Events:      e.events.Snapshot(),
	}, nil
}

// ExportDocument builds the bounded diagnostic archive: the status snapshot plus
// recent failure records and the event log. It contains actionable information
// without unbounded memory growth.
func (e *Engine) ExportDocument(ctx context.Context, limit int) (*ExportDocument, error) {
	e.mu.Lock()
	cfg := e.cfg
	e.mu.Unlock()

	windowStart := time.Now().Add(-time.Duration(cfg.WindowHours) * time.Hour).Unix()
	accounts := e.statusAccounts(cfg, windowStart)
	return &ExportDocument{
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Plugin:     ExportPlugin{Name: PluginName, Version: PluginVersion},
		Config: ExportConfig{
			StatusConfig:    newStatusConfig(cfg),
			FailurePatterns: append([]string(nil), cfg.FailurePatterns...),
		},
		Accounts:       accounts,
		RecentFailures: e.failures.Snapshot(limit),
		Events:         e.events.Snapshot(),
	}, nil
}

func newStatusConfig(cfg config.Config) StatusConfig {
	return StatusConfig{
		AuthType:             cfg.AuthType,
		Proxies:              append([]string(nil), cfg.Proxies...),
		WindowHours:          cfg.WindowHours,
		FailurePatternsCount: len(cfg.FailurePatterns),
	}
}

// statusAccounts collects the managed credentials and their per-proxy stats.
func (e *Engine) statusAccounts(cfg config.Config, windowStart int64) []StatusAccount {
	entries := e.authEntries()
	accounts := make([]StatusAccount, 0, len(entries))
	for _, entry := range entries {
		if entry.RuntimeOnly || entry.Path == "" || entry.ID == "" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(entry.Type), cfg.AuthType) {
			continue
		}

		e.mu.Lock()
		st, exists := e.auths[entry.ID]
		var current string
		var stats map[string]scorer.Stats
		if exists {
			current = st.currentProxy
			stats = st.allStats(windowStart)
		}
		observations := e.statusObservations(cfg, current, stats, st)
		e.mu.Unlock()

		var totalSuccess, totalFailed int64
		for _, obs := range observations {
			totalSuccess += obs.LongSuccess
			totalFailed += (obs.LongTotal - obs.LongSuccess)
		}

		accounts = append(accounts, StatusAccount{
			AuthID:       entry.ID,
			AuthIndex:    entry.Index,
			Type:         entry.Type,
			Path:         entry.Path,
			CurrentProxy: current,
			AppliedProxy: current,
			StateProxy:   current,
			TotalSuccess: totalSuccess,
			TotalFailed:  totalFailed,
			Observations: observations,
		})
	}
	return accounts
}

// statusObservations builds one row per interesting proxy: every configured
// proxy plus the current proxy and the default pseudo proxy, deduplicated
// and ordered by configuration order first. Must be called with e.mu held.
func (e *Engine) statusObservations(cfg config.Config, current string, stats map[string]scorer.Stats, st *authMemoryState) []ProxyObservation {
	order := make([]string, 0, len(cfg.Proxies)+2)
	seen := make(map[string]bool, len(cfg.Proxies)+2)
	appendProxy := func(proxy string) {
		if proxy == "" || seen[proxy] {
			return
		}
		seen[proxy] = true
		order = append(order, proxy)
	}
	for _, proxy := range cfg.Proxies {
		appendProxy(proxy)
	}
	appendProxy(current)
	appendProxy(scorer.DefaultProxy)
	observations := make([]ProxyObservation, 0, len(order))
	for _, proxy := range order {
		var lastSeen int64
		var lastOK bool
		if st != nil {
			if pStats, hasP := st.stats[proxy]; hasP {
				lastSeen = pStats.lastSeen
				lastOK = pStats.lastOK
			}
		}
		stat := stats[proxy]
		observations = append(observations, ProxyObservation{
			Proxy:         proxy,
			RecentSuccess: stat.RecentSuccess,
			RecentTotal:   stat.RecentTotal,
			LongSuccess:   stat.LongSuccess,
			LongTotal:     stat.LongTotal,
			Score:         scorer.Score(stat),
			LastSeen:      lastSeen,
			LastOK:        lastOK,
		})
	}
	return observations
}
