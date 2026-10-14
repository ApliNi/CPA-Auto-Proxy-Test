package plugin

import (
	"context"
	"encoding/json"
	"strings"
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

	engine.RecordObservation("a.json", "http://p1", now-100000, true)
	engine.RecordObservation("a.json", scorer.DefaultProxy, now-95000, false)
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
			AuthID        string `json:"auth_id"`
			AuthIndex     string `json:"auth_index"`
			Type          string `json:"type"`
			AppliedProxy  string `json:"applied_proxy"`
			StateProxy    string `json:"state_proxy"`
			RecentSuccess int64  `json:"recent_success"`
			RecentFailed  int64  `json:"recent_failed"`
			TotalSuccess  int64  `json:"total_success"`
			TotalFailed   int64  `json:"total_failed"`
			Observations  []struct {
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
		ProxySummaries []struct {
			Proxy         string `json:"proxy"`
			TotalSuccess  int64  `json:"total_success"`
			TotalFailed   int64  `json:"total_failed"`
			RecentSuccess int64  `json:"recent_success"`
			RecentFailed  int64  `json:"recent_failed"`
		} `json:"proxy_summaries"`
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
	if doc.Accounts[a].RecentSuccess != 2 || doc.Accounts[a].RecentFailed != 1 {
		t.Fatalf("a.json recent success/failed = %d/%d, want 2/1", doc.Accounts[a].RecentSuccess, doc.Accounts[a].RecentFailed)
	}
	if doc.Accounts[a].TotalSuccess != 3 || doc.Accounts[a].TotalFailed != 2 {
		t.Fatalf("a.json total success/failed = %d/%d, want 3/2", doc.Accounts[a].TotalSuccess, doc.Accounts[a].TotalFailed)
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
	if p1obs.RecentSuccess != 1 || p1obs.RecentTotal != 2 || p1obs.LongSuccess != 2 || p1obs.LongTotal != 3 {
		t.Fatalf("p1 stats = %+v", p1obs)
	}
	want := scorer.Score(scorer.Stats{RecentSuccess: 1, RecentTotal: 2, LongSuccess: 2, LongTotal: 3})
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
	if doc.Accounts[a].Observations[def].LongTotal != 2 {
		t.Fatalf("default stats = %+v", doc.Accounts[a].Observations[def])
	}

	b, okB := byID["b.json"]
	if !okB {
		t.Fatalf("account b.json missing: %+v", doc.Accounts)
	}
	if doc.Accounts[b].StateProxy != "http://p2" {
		t.Fatalf("b.json state_proxy = %q, want http://p2", doc.Accounts[b].StateProxy)
	}
	if doc.Accounts[b].RecentSuccess != 1 || doc.Accounts[b].RecentFailed != 0 {
		t.Fatalf("b.json recent success/failed = %d/%d, want 1/0", doc.Accounts[b].RecentSuccess, doc.Accounts[b].RecentFailed)
	}
	if doc.Accounts[b].TotalSuccess != 1 || doc.Accounts[b].TotalFailed != 0 {
		t.Fatalf("b.json total success/failed = %d/%d, want 1/0", doc.Accounts[b].TotalSuccess, doc.Accounts[b].TotalFailed)
	}
	// p2 is a configured proxy, so a.json carries a zero-count row for it.
	for _, obs := range doc.Accounts[a].Observations {
		if obs.Proxy == "http://p2" && (obs.RecentTotal != 0 || obs.LongTotal != 0) {
			t.Fatalf("a.json p2 row must be zero-count: %+v", obs)
		}
	}
	if len(doc.ProxySummaries) != 2 {
		t.Fatalf("proxy_summaries len = %d, want 2: %+v", len(doc.ProxySummaries), doc.ProxySummaries)
	}
	if doc.ProxySummaries[0].Proxy != "http://p1" || doc.ProxySummaries[0].RecentSuccess != 1 || doc.ProxySummaries[0].RecentFailed != 1 ||
		doc.ProxySummaries[0].TotalSuccess != 2 || doc.ProxySummaries[0].TotalFailed != 1 {
		t.Fatalf("p1 summary = %+v, want rec=1/1 tot=2/1", doc.ProxySummaries[0])
	}
	if doc.ProxySummaries[1].Proxy != "http://p2" || doc.ProxySummaries[1].RecentSuccess != 0 || doc.ProxySummaries[1].RecentFailed != 0 ||
		doc.ProxySummaries[1].TotalSuccess != 0 || doc.ProxySummaries[1].TotalFailed != 0 {
		t.Fatalf("p2 summary = %+v, want all 0", doc.ProxySummaries[1])
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
		ProxySummaries []ProxySummary  `json:"proxy_summaries"`
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
	if len(doc.ProxySummaries) != 1 || doc.ProxySummaries[0].Proxy != "http://p1" {
		t.Fatalf("export proxy_summaries = %+v", doc.ProxySummaries)
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

func TestStatusPageHTMLContract(t *testing.T) {
	page := StatusPageHTML()
	if len(page) == 0 {
		t.Fatal("StatusPageHTML returned empty string")
	}

	requiredSubstrings := []string{
		"<!DOCTYPE html>",
		"CPA-Auto-Proxy-Test 代理路由状态",
		"/v0/management/cpa-auto-proxy-test",
		"management_key",
		"enc::",
		"Authorization",
		"X-Management-Key",
		"downloadExport",
		"proxy_summaries",
		".proxy-item",
	}
	for _, req := range requiredSubstrings {
		if !strings.Contains(page, req) {
			t.Errorf("StatusPageHTML missing required token: %q", req)
		}
	}

	forbiddenSubstrings := []string{
		"/v0/resource/plugins/cpa-auto-proxy-test/status",
		"/v0/resource/plugins/cpa-auto-proxy-test/export",
		"fetch('data'",
		`fetch("data"`,
		`<a href="export"`,
		"auth-prompt",
		"auth-key-input",
		"saveKeyAndReload",
	}
	for _, f := range forbiddenSubstrings {
		if strings.Contains(page, f) {
			t.Errorf("StatusPageHTML contains forbidden token: %q", f)
		}
	}
}
func TestNotConfiguredHTMLContent(t *testing.T) {
	page := NotConfiguredHTML()
	if !strings.Contains(page, "cpa-auto-proxy-test") || !strings.Contains(page, "插件尚未加载配置") {
		t.Fatalf("NotConfiguredHTML missing expected content: %s", page)
	}
}

func TestManagementResponseBuilders(t *testing.T) {
	jsonResp := JsonManagementResponse(200, []byte(`{"ok":true}`))
	if jsonResp.StatusCode != 200 {
		t.Errorf("JsonManagementResponse StatusCode = %d, want 200", jsonResp.StatusCode)
	}
	if ct := jsonResp.Headers["Content-Type"]; len(ct) == 0 || !strings.Contains(ct[0], "application/json") {
		t.Errorf("JsonManagementResponse Content-Type = %v, want application/json", ct)
	}
	if string(jsonResp.Body) != `{"ok":true}` {
		t.Errorf("JsonManagementResponse Body = %q, want {\"ok\":true}", string(jsonResp.Body))
	}

	htmlResp := HtmlManagementResponse(200, "<html>test</html>")
	if htmlResp.StatusCode != 200 {
		t.Errorf("HtmlManagementResponse StatusCode = %d, want 200", htmlResp.StatusCode)
	}
	if ct := htmlResp.Headers["Content-Type"]; len(ct) == 0 || !strings.Contains(ct[0], "text/html") {
		t.Errorf("HtmlManagementResponse Content-Type = %v, want text/html", ct)
	}
	if string(htmlResp.Body) != "<html>test</html>" {
		t.Errorf("HtmlManagementResponse Body = %q, want <html>test</html>", string(htmlResp.Body))
	}
}
func TestProxySummariesMultiAccountAggregation(t *testing.T) {
	host := newHostStub()
	host.addAuth(t, "acc1.json", "idx-1", "Antigravity", "")
	host.addAuth(t, "acc2.json", "idx-2", "Antigravity", "")
	cfg := baseConfig(t, "http://p1", "http://p2", "http://p3")
	engine := newTestEngine(t, cfg, host)

	now := time.Now().Unix()
	// acc1:
	// p1: 1 historical success (now-100000), 1 recent success (now-10), 1 recent failure (now-5)
	// => p1 for acc1: recOk=1, recFail=1; totOk=2, totFail=1
	engine.RecordObservation("acc1.json", "http://p1", now-100000, true)
	engine.RecordObservation("acc1.json", "http://p1", now-10, true)
	engine.RecordObservation("acc1.json", "http://p1", now-5, false)

	// p2 for acc1: 2 recent successes
	// => p2 for acc1: recOk=2, recFail=0; totOk=2, totFail=0
	engine.RecordObservation("acc1.json", "http://p2", now-20, true)
	engine.RecordObservation("acc1.json", "http://p2", now-15, true)

	// default proxy for acc1 (must not leak into p1/p2/p3 summaries)
	engine.RecordObservation("acc1.json", scorer.DefaultProxy, now-10, false)

	// acc2:
	// p1: 2 recent failures (now-8, now-2)
	// => p1 for acc2: recOk=0, recFail=2; totOk=0, totFail=2
	engine.RecordObservation("acc2.json", "http://p1", now-8, false)
	engine.RecordObservation("acc2.json", "http://p1", now-2, false)

	// p2 for acc2: 1 historical fail (now-100000), 1 recent success (now-1)
	// => p2 for acc2: recOk=1, recFail=0; totOk=1, totFail=1
	engine.RecordObservation("acc2.json", "http://p2", now-100000, false)
	engine.RecordObservation("acc2.json", "http://p2", now-1, true)

	// p3: no observations for either account

	snap, err := engine.StatusSnapshot(context.Background())
	if err != nil {
		t.Fatalf("StatusSnapshot error = %v", err)
	}

	if len(snap.ProxySummaries) != 3 {
		t.Fatalf("expected 3 proxy summaries, got %d", len(snap.ProxySummaries))
	}

	// p1: acc1(recOk=1, recFail=1, totOk=2, totFail=1) + acc2(recOk=0, recFail=2, totOk=0, totFail=2)
	// => recOk=1, recFail=3, totOk=2, totFail=3
	p1 := snap.ProxySummaries[0]
	if p1.Proxy != "http://p1" || p1.RecentSuccess != 1 || p1.RecentFailed != 3 || p1.TotalSuccess != 2 || p1.TotalFailed != 3 {
		t.Fatalf("p1 summary unexpected: %+v", p1)
	}

	// p2: acc1(recOk=2, recFail=0, totOk=2, totFail=0) + acc2(recOk=1, recFail=0, totOk=1, totFail=1)
	// => recOk=3, recFail=0, totOk=3, totFail=1
	p2 := snap.ProxySummaries[1]
	if p2.Proxy != "http://p2" || p2.RecentSuccess != 3 || p2.RecentFailed != 0 || p2.TotalSuccess != 3 || p2.TotalFailed != 1 {
		t.Fatalf("p2 summary unexpected: %+v", p2)
	}

	// p3: all 0
	p3 := snap.ProxySummaries[2]
	if p3.Proxy != "http://p3" || p3.RecentSuccess != 0 || p3.RecentFailed != 0 || p3.TotalSuccess != 0 || p3.TotalFailed != 0 {
		t.Fatalf("p3 summary unexpected: %+v", p3)
	}

	// Empty proxies config test
	emptyCfg := cfg
	emptyCfg.Proxies = nil
	emptySummaries := summarizeProxies(emptyCfg, snap.Accounts)
	if len(emptySummaries) != 0 {
		t.Fatalf("expected 0 summaries for empty proxies config, got %d", len(emptySummaries))
	}
}
