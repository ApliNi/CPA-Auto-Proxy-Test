package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"

	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/config"
	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/scorer"
)

type hostStub struct {
	mu      sync.Mutex
	entries []AuthEntry
	files   map[string]string
	logs    []string
}

func newHostStub() *hostStub {
	return &hostStub{files: make(map[string]string)}
}

func (h *hostStub) addAuth(t *testing.T, name string, index string, authType string, proxyURL string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	raw := `{"type":"` + authType + `","access_token":"tok"`
	if proxyURL != "" {
		raw += `,"proxy_url":"` + proxyURL + `"`
	}
	raw += `}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write stub file: %v", err)
	}
	entry := AuthEntry{
		ID:    name,
		Index: index,
		Type:  authType,
		Path:  path,
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, entry)
	h.files[index] = path
}

func (h *hostStub) AuthList() ([]AuthEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]AuthEntry(nil), h.entries...), nil
}

func (h *hostStub) Log(level string, message string, fields map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, level+": "+message)
}

func (h *hostStub) fileProxy(t *testing.T, index string) string {
	t.Helper()
	h.mu.Lock()
	path := h.files[index]
	h.mu.Unlock()
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatalf("read stub file: %v", errRead)
	}
	marker := `"proxy_url":"`
	start := strings.Index(string(raw), marker)
	if start < 0 {
		return ""
	}
	rest := string(raw)[start+len(marker):]
	end := strings.Index(rest, `"`)
	return rest[:end]
}

func baseConfig(t *testing.T, proxies ...string) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Proxies = proxies
	cfg.FailurePatterns = []string{"400", "User location is not supported"}
	return cfg
}

func newTestEngine(t *testing.T, cfg config.Config, host *hostStub) *Engine {
	t.Helper()
	engine, err := New(cfg, host)
	if err != nil {
		t.Fatalf("New engine error = %v", err)
	}
	t.Cleanup(func() {
		if errClose := engine.Close(); errClose != nil {
			t.Errorf("Close engine error = %v", errClose)
		}
	})
	return engine
}

func usage(authIndex string, failed bool, statusCode int, body string) pluginapi.UsageRecord {
	return pluginapi.UsageRecord{
		RequestID: "req-1",
		Provider:  "antigravity",
		AuthID:    "cred-" + authIndex,
		AuthIndex: authIndex,
		AuthType:  "oauth",
		Failed:    failed,
		Failure:   pluginapi.UsageFailure{StatusCode: statusCode, Body: body},
	}
}

// usageWithProvider lets tests control the provider field, mirroring the CPA
// executor identifier that the engine filters on.
func usageWithProvider(authIndex string, provider string, failed bool, statusCode int, body string) pluginapi.UsageRecord {
	record := usage(authIndex, failed, statusCode, body)
	record.Provider = provider
	return record
}

func TestHandleUsageIgnoresOtherAuthTypes(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "gemini.json", "idx-1", "Gemini", "")
	engine := newTestEngine(t, baseConfig(t, "http://p1"), host)
	engine.HandleUsage(usageWithProvider("idx-1", "gemini", true, 400, "User location is not supported"))
	stats := engine.AuthStats("cred-idx-1", 0)
	if len(stats) != 0 {
		t.Fatalf("unmanaged auth type recorded observations: %+v", stats)
	}
}

func TestHandleUsageIgnoresUnmatchedFailure(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "cred.json", "idx-1", "Antigravity", "")
	engine := newTestEngine(t, baseConfig(t, "http://p1"), host)
	record := usage("idx-1", true, 503, "service unavailable")
	engine.HandleUsage(record)
	stats := engine.AuthStats("cred-idx-1", 0)
	if len(stats) != 0 {
		t.Fatalf("unmatched failure recorded: %+v", stats)
	}
}

func TestFirstSuccessAppliesBestProxyImmediately(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "cred.json", "idx-1", "Antigravity", "")
	engine := newTestEngine(t, baseConfig(t, "http://p1", "http://p2"), host)
	engine.HandleUsage(usage("idx-1", false, 0, ""))
	// A success while still on the global default applies the first untried
	// proxy instead of waiting for a failure.
	if got := host.fileProxy(t, "idx-1"); got != "http://p1" {
		t.Fatalf("file proxy = %q, want http://p1", got)
	}
	state := engine.AuthState("cred-idx-1")
	if state != "http://p1" {
		t.Fatalf("auth state = %q, want http://p1", state)
	}
	stats := engine.AuthStats("cred-idx-1", 0)
	if stats[scorer.DefaultProxy].LongTotal != 1 || stats[scorer.DefaultProxy].LongSuccess != 1 {
		t.Fatalf("default observation missing: %+v", stats)
	}
}

func TestFailureSwitchesToNextUntriedProxy(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "cred.json", "idx-1", "Antigravity", "http://p1")
	engine := newTestEngine(t, baseConfig(t, "http://p1", "http://p2", "http://p3"), host)
	engine.HandleUsage(usage("idx-1", true, 400, `{"message":"User location is not supported"}`))
	if got := host.fileProxy(t, "idx-1"); got != "http://p2" {
		t.Fatalf("file proxy = %q, want http://p2 after p1 failure", got)
	}
}

func TestFailureSwitchesToBestScoredProxy(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "cred.json", "idx-1", "Antigravity", "http://p2")
	engine := newTestEngine(t, baseConfig(t, "http://p1", "http://p2"), host)
	now := time.Now().Unix()
	engine.RecordObservation("cred-idx-1", "http://p1", now-10, true)
	engine.RecordObservation("cred-idx-1", "http://p1", now-9, true)
	engine.RecordObservation("cred-idx-1", "http://p1", now-8, false)
	engine.HandleUsage(usage("idx-1", true, 400, "User location is not supported"))
	if got := host.fileProxy(t, "idx-1"); got != "http://p1" {
		t.Fatalf("file proxy = %q, want best-scored http://p1", got)
	}
}

func TestSuccessOnAppliedProxyDoesNotSwitch(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "cred.json", "idx-1", "Antigravity", "http://p1")
	engine := newTestEngine(t, baseConfig(t, "http://p1", "http://p2"), host)
	engine.HandleUsage(usage("idx-1", false, 0, ""))
	if got := host.fileProxy(t, "idx-1"); got != "http://p1" {
		t.Fatalf("file proxy = %q, want unchanged http://p1", got)
	}
}

func TestCleanupRemovesStaleAuthsAndProxies(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "keep.json", "idx-keep", "Antigravity", "")
	engine := newTestEngine(t, baseConfig(t, "http://kept"), host)
	now := time.Now().Unix()
	engine.RecordObservation("removed.json", "http://kept", now, true)
	engine.RecordObservation("removed.json", "http://dropped", now, false)
	engine.RecordObservation("keep.json", "http://dropped", now, false)
	if errCleanup := engine.Cleanup(context.Background()); errCleanup != nil {
		t.Fatalf("Cleanup error = %v", errCleanup)
	}
	stats := engine.AuthStats("removed.json", 0)
	if len(stats) != 0 {
		t.Fatalf("stale auth records survived cleanup: %+v", stats)
	}
}

func TestResolveAuthInfoFallsBackToHostEntry(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "byindex.json", "idx-9", "Antigravity", "")
	engine := newTestEngine(t, baseConfig(t, "http://p1"), host)
	record := usage("idx-9", false, 0, "")
	record.AuthID = ""
	engine.HandleUsage(record)
	stats := engine.AuthStats("byindex.json", 0)
	if stats[scorer.DefaultProxy].LongTotal != 1 {
		t.Fatalf("fallback auth id not used: %+v", stats)
	}
}

func TestReconfigureUpdatesMatcher(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "cred.json", "idx-1", "Antigravity", "http://p1")
	cfg := baseConfig(t, "http://p1")
	engine := newTestEngine(t, cfg, host)
	updated := cfg
	updated.FailurePatterns = []string{"500"}
	if errReconfigure := engine.Reconfigure(updated); errReconfigure != nil {
		t.Fatalf("Reconfigure error = %v", errReconfigure)
	}
	engine.HandleUsage(usage("idx-1", true, 400, "User location is not supported"))
	stats := engine.AuthStats("cred-idx-1", 0)
	if len(stats) != 0 {
		t.Fatalf("pattern from old config still active: %+v", stats)
	}
	engine.HandleUsage(usage("idx-1", true, 500, "boom"))
	stats = engine.AuthStats("cred-idx-1", 0)
	if stats["http://p1"].LongTotal != 1 {
		t.Fatalf("new pattern not applied: %+v", stats)
	}
}

func TestEmptyFailurePatternsMatchesAllFailures(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "cred.json", "idx-1", "Antigravity", "http://p1")
	cfg := baseConfig(t, "http://p1", "http://p2")
	cfg.FailurePatterns = nil // empty matches all failures
	engine := newTestEngine(t, cfg, host)

	// A failure with any status code (e.g. 503) should trigger switch to p2
	engine.HandleUsage(usage("idx-1", true, 503, "internal server error"))
	if got := host.fileProxy(t, "idx-1"); got != "http://p2" {
		t.Fatalf("file proxy = %q, want http://p2 after empty pattern failure switch", got)
	}
}

func TestInitialStartupClearsObsoleteProxyFromFile(t *testing.T) {
	host := newHostStub()
	// cred-1 has an obsolete proxy not in active config
	host.addAuth(t, "cred-1.json", "idx-1", "Antigravity", "http://old-bad-proxy:8080")
	// cred-2 has a valid proxy in active config
	host.addAuth(t, "cred-2.json", "idx-2", "Antigravity", "http://p1")

	cfg := baseConfig(t, "http://p1", "http://p2")
	_ = newTestEngine(t, cfg, host)

	// cred-1's obsolete proxy should have been physically wiped from the file
	if got := host.fileProxy(t, "idx-1"); got != "" {
		t.Fatalf("file proxy for idx-1 = %q, want empty (cleared)", got)
	}
	// cred-2's valid proxy should be retained
	if got := host.fileProxy(t, "idx-2"); got != "http://p1" {
		t.Fatalf("file proxy for idx-2 = %q, want http://p1", got)
	}
}

func TestReconfigureClearsObsoleteProxyFromFile(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "cred.json", "idx-1", "Antigravity", "http://p2")

	// Initially p2 is valid
	cfg := baseConfig(t, "http://p1", "http://p2")
	engine := newTestEngine(t, cfg, host)
	if got := host.fileProxy(t, "idx-1"); got != "http://p2" {
		t.Fatalf("file proxy initially = %q, want http://p2", got)
	}

	// Reconfigure with p2 removed
	updated := cfg
	updated.Proxies = []string{"http://p1"}
	if errReconf := engine.Reconfigure(updated); errReconf != nil {
		t.Fatalf("Reconfigure failed: %v", errReconf)
	}

	// cred's proxy should now be physically cleared from file
	if got := host.fileProxy(t, "idx-1"); got != "" {
		t.Fatalf("file proxy after reconfigure = %q, want empty (cleared)", got)
	}
}
