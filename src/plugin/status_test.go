package plugin

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/scorer"
)

func TestStatusSnapshotFields(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "a.json", "idx-1", "Antigravity", "http://p1")
	host.addAuth(t, "b.json", "idx-2", "antigravity", "")
	host.addAuth(t, "other.json", "idx-3", "Gemini", "")
	engine := newTestEngine(t, baseConfig(t, "http://p1", "http://p2"), host)
	ctx := context.Background()
	now := time.Now().Unix()

	engine.RecordObservation("a.json", "http://p1", now-30, true)
	engine.RecordObservation("a.json", "http://p1", now-20, false)
	engine.RecordObservation("a.json", scorer.DefaultProxy, now-10, true)
	engine.RecordObservation("b.json", scorer.DefaultProxy, now-5, true)
	engine.SetAuthState("b.json", "http://p2")

	snapshot, errBuild := engine.StatusSnapshot(ctx)
	if errBuild != nil {
		t.Fatalf("StatusSnapshot error = %v", errBuild)
	}
	raw, errMarshal := json.Marshal(snapshot)
	if errMarshal != nil {
		t.Fatalf("marshal snapshot error = %v", errMarshal)
	}
	var doc struct {
		GeneratedAt string `json:"generated_at"`
		Config      struct {
			AuthType             string   `json:"auth_type"`
			Proxies              []string `json:"proxies"`
			WindowHours          int      `json:"window_hours"`
			FailurePatternsCount int      `json:"failure_patterns_count"`
		} `json:"config"`
		Accounts []struct {
			AuthID       string `json:"auth_id"`
			AuthIndex    string `json:"auth_index"`
			Type         string `json:"type"`
			AppliedProxy string `json:"applied_proxy"`
			StateProxy   string `json:"state_proxy"`
			TotalSuccess int64  `json:"total_success"`
			TotalFailed  int64  `json:"total_failed"`
			Observations []struct {
				Proxy         string  `json:"proxy"`
				RecentSuccess int64   `json:"recent_success"`
				RecentTotal   int64   `json:"recent_total"`
				LongSuccess   int64   `json:"long_success"`
				LongTotal     int64   `json:"long_total"`
				Score         float64 `json:"score"`
				LastSeen      int64   `json:"last_seen"`
				LastOK        bool    `json:"last_ok"`
			} `json:"observations"`
		} `json:"accounts"`
	}
	if errUnmarshal := json.Unmarshal(raw, &doc); errUnmarshal != nil {
		t.Fatalf("unmarshal snapshot error = %v", errUnmarshal)
	}
	if doc.GeneratedAt == "" {
		t.Fatal("generated_at is empty")
	}
	if _, errTime := time.Parse(time.RFC3339, doc.GeneratedAt); errTime != nil {
		t.Fatalf("generated_at not RFC3339: %v", errTime)
	}
	if doc.Config.AuthType != "Antigravity" || doc.Config.WindowHours != 24 {
		t.Fatalf("config summary = %+v", doc.Config)
	}
	if doc.Config.FailurePatternsCount != 2 || len(doc.Config.Proxies) != 2 {
		t.Fatalf("config summary = %+v", doc.Config)
	}
	// Other auth types and entries without an ID are excluded; two remain.
	if len(doc.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2: %+v", len(doc.Accounts), doc.Accounts)
	}

	byID := map[string]int{}
	for i, account := range doc.Accounts {
		byID[account.AuthID] = i
	}
	a, okA := byID["a.json"]
	if !okA {
		t.Fatalf("account a.json missing: %+v", doc.Accounts)
	}
	if doc.Accounts[a].AppliedProxy != "http://p1" {
		t.Fatalf("a.json applied_proxy = %q, want http://p1", doc.Accounts[a].AppliedProxy)
	}
	if doc.Accounts[a].TotalSuccess != 2 || doc.Accounts[a].TotalFailed != 1 {
		t.Fatalf("a.json total success/failed = %d/%d, want 2/1", doc.Accounts[a].TotalSuccess, doc.Accounts[a].TotalFailed)
	}
	proxies := map[string]int{}
	for i, obs := range doc.Accounts[a].Observations {
		proxies[obs.Proxy] = i
	}
	p1, okP1 := proxies["http://p1"]
	if !okP1 {
		t.Fatalf("a.json missing http://p1 observation: %+v", doc.Accounts[a].Observations)
	}
	p1obs := doc.Accounts[a].Observations[p1]
	if p1obs.RecentSuccess != 1 || p1obs.RecentTotal != 2 || p1obs.LongSuccess != 1 || p1obs.LongTotal != 2 {
		t.Fatalf("p1 stats = %+v", p1obs)
	}
	want := scorer.Score(scorer.Stats{RecentSuccess: 1, RecentTotal: 2, LongSuccess: 1, LongTotal: 2})
	if p1obs.Score != want {
		t.Fatalf("p1 score = %v, want %v", p1obs.Score, want)
	}
	if p1obs.LastSeen == 0 || p1obs.LastOK {
		t.Fatalf("p1 last observation = %+v, want failed with timestamp", p1obs)
	}
	def, okDef := proxies[scorer.DefaultProxy]
	if !okDef {
		t.Fatalf("a.json missing default observation: %+v", doc.Accounts[a].Observations)
	}
	if doc.Accounts[a].Observations[def].LongTotal != 1 {
		t.Fatalf("default stats = %+v", doc.Accounts[a].Observations[def])
	}

	b, okB := byID["b.json"]
	if !okB {
		t.Fatalf("account b.json missing: %+v", doc.Accounts)
	}
	if doc.Accounts[b].StateProxy != "http://p2" {
		t.Fatalf("b.json state_proxy = %q, want http://p2", doc.Accounts[b].StateProxy)
	}
	// p2 is a configured proxy, so a.json carries a zero-count row for it.
	for _, obs := range doc.Accounts[a].Observations {
		if obs.Proxy == "http://p2" && (obs.RecentTotal != 0 || obs.LongTotal != 0) {
			t.Fatalf("a.json p2 row must be zero-count: %+v", obs)
		}
	}
}

func TestExportDocumentShape(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "a.json", "idx-1", "Antigravity", "")
	engine := newTestEngine(t, baseConfig(t, "http://p1"), host)
	ctx := context.Background()
	for i := range 3 {
		engine.failures.Add(FailureRecord{
			TS:           time.Now().Unix() - int64(10-i),
			AuthID:       "a.json",
			Proxy:        scorer.DefaultProxy,
			StatusCode:   400 + i,
			ErrorSnippet: "failure details",
		})
	}
	engine.AddEvent("info", "switch", "a.json", "switched proxy from default to http://p1")

	document, errBuild := engine.ExportDocument(ctx, 2)
	if errBuild != nil {
		t.Fatalf("ExportDocument error = %v", errBuild)
	}
	raw, errMarshal := json.Marshal(document)
	if errMarshal != nil {
		t.Fatalf("marshal export error = %v", errMarshal)
	}
	var doc struct {
		ExportedAt string `json:"exported_at"`
		Plugin     struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"plugin"`
		Config struct {
			FailurePatterns []string `json:"failure_patterns"`
		} `json:"config"`
		RecentFailures []FailureRecord `json:"recent_failures"`
		Events         []Event         `json:"events"`
	}
	if errUnmarshal := json.Unmarshal(raw, &doc); errUnmarshal != nil {
		t.Fatalf("unmarshal export error = %v", errUnmarshal)
	}
	if doc.Plugin.Name != PluginName || doc.Plugin.Version == "" {
		t.Fatalf("plugin identity = %+v", doc.Plugin)
	}
	if len(doc.RecentFailures) != 2 {
		t.Fatalf("recent_failures = %d, want 2 (limit)", len(doc.RecentFailures))
	}
	if doc.RecentFailures[0].TS < doc.RecentFailures[1].TS {
		t.Fatalf("recent_failures not newest first: %+v", doc.RecentFailures)
	}
	if len(doc.Config.FailurePatterns) != 2 {
		t.Fatalf("failure_patterns = %+v", doc.Config.FailurePatterns)
	}
	if len(doc.Events) != 1 || doc.Events[0].Kind != "switch" {
		t.Fatalf("events = %+v", doc.Events)
	}
}

func TestEventBufferCapacityAndOrder(t *testing.T) {
	buf := newEventBuffer()
	for i := range eventBufferCapacity + 5 {
		buf.Add(Event{TS: int64(i), Level: "info", Kind: "usage", Message: "event"})
	}
	snapshot := buf.Snapshot()
	if len(snapshot) != eventBufferCapacity {
		t.Fatalf("snapshot len = %d, want %d", len(snapshot), eventBufferCapacity)
	}
	// Newest first: the last added event must lead.
	if snapshot[0].TS != eventBufferCapacity+4 {
		t.Fatalf("newest event TS = %d, want %d", snapshot[0].TS, eventBufferCapacity+4)
	}
	if snapshot[len(snapshot)-1].TS != 5 {
		t.Fatalf("oldest kept event TS = %d, want 5", snapshot[len(snapshot)-1].TS)
	}
}

func TestEventBufferConcurrentWrites(t *testing.T) {
	buf := newEventBuffer()
	const writers = 8
	const perWriter = 50
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := range perWriter {
				buf.Add(Event{TS: int64(i), Level: "info", Kind: "usage", Message: "concurrent"})
				_ = writer
			}
		}(w)
	}
	wg.Wait()
	snapshot := buf.Snapshot()
	if len(snapshot) != eventBufferCapacity {
		t.Fatalf("snapshot len = %d, want %d", len(snapshot), eventBufferCapacity)
	}
	// Ring order is by insertion, so concurrent writers interleave; only the
	// oldest-dropped invariant must hold: the newest insertion (TS is not
	// monotonic across writers, so just check the buffer is bounded and
	// every entry is present in the expected payload set).
	for _, ev := range snapshot {
		if ev.Message != "concurrent" || ev.Level != "info" || ev.Kind != "usage" {
			t.Fatalf("unexpected event = %+v", ev)
		}
	}
}

func TestHandleUsageRecordsEvents(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "a.json", "idx-1", "Antigravity", "")
	engine := newTestEngine(t, baseConfig(t, "http://p1", "http://p2"), host)

	// A provider-matched record without resolvable auth info records a warn.
	orphan := usageWithProvider("idx-404", "antigravity", true, 400, "User location is not supported")
	orphan.AuthID = ""
	orphan.AuthIndex = "idx-404"
	engine.HandleUsage(orphan)
	events := engine.events.Snapshot()
	if len(events) != 1 {
		t.Fatalf("events after orphan usage = %+v, want 1", events)
	}
	if events[0].Level != "warn" || events[0].Kind != "usage" {
		t.Fatalf("orphan event = %+v, want warn/usage", events[0])
	}

	// A matching failure switches to an untried proxy and records the switch.
	engine.HandleUsage(usage("idx-1", true, 400, "User location is not supported"))
	events = engine.events.Snapshot()
	if len(events) != 2 {
		t.Fatalf("events after switch = %+v, want 2", events)
	}
	if events[0].Kind != "switch" || events[0].Level != "info" {
		t.Fatalf("switch event = %+v, want info/switch", events[0])
	}
	if events[0].AuthID != "cred-idx-1" {
		t.Fatalf("switch event auth_id = %q, want cred-idx-1", events[0].AuthID)
	}
}
