package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/plugin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type testHost struct{}

func (testHost) AuthList() ([]plugin.AuthEntry, error) {
	return nil, nil
}

func (testHost) Log(level string, message string, fields map[string]any) {}

func setupTest(t *testing.T) {
	t.Helper()
	activeHost = testHost{}
	shutdownEngine()
	t.Cleanup(func() {
		shutdownEngine()
	})
}

// simulateHostValidPlugin replicates internal/pluginhost/host.go validPlugin logic.
func simulateHostValidPlugin(meta pluginapi.Metadata, caps registrationCapability) bool {
	if strings.TrimSpace(meta.Name) == "" {
		return false
	}
	if strings.TrimSpace(meta.Version) == "" {
		return false
	}
	if strings.TrimSpace(meta.Author) == "" {
		return false
	}
	if strings.TrimSpace(meta.GitHubRepository) == "" {
		return false
	}
	return caps.UsagePlugin || caps.ManagementAPI
}

func TestPluginRegisterAndReconfigure(t *testing.T) {
	setupTest(t)

	initialConfigYAML := `
auth-type: "Antigravity"
proxies:
  - "http://127.0.0.1:7890"
failure-patterns:
  - "400"
window-hours: 12
`
	registerReq, errMarshal := json.Marshal(lifecycleRequest{
		ConfigYAML:    []byte(initialConfigYAML),
		SchemaVersion: pluginabi.SchemaVersion,
	})
	if errMarshal != nil {
		t.Fatalf("marshal register request: %v", errMarshal)
	}

	// 1. Initial register call
	regRaw, errHandle := handleMethod(pluginabi.MethodPluginRegister, registerReq)
	if errHandle != nil {
		t.Fatalf("handleMethod register: %v", errHandle)
	}
	var regEnv envelope
	if errUnmarshal := json.Unmarshal(regRaw, &regEnv); errUnmarshal != nil {
		t.Fatalf("unmarshal register envelope: %v", errUnmarshal)
	}
	if !regEnv.OK {
		t.Fatalf("register envelope not ok: %+v", regEnv.Error)
	}

	var reg registration
	if errUnmarshal := json.Unmarshal(regEnv.Result, &reg); errUnmarshal != nil {
		t.Fatalf("unmarshal registration result: %v", errUnmarshal)
	}
	if !simulateHostValidPlugin(reg.Metadata, reg.Capabilities) {
		t.Fatalf("register result failed host validPlugin check: %+v", reg)
	}
	if reg.Metadata.Name != pluginIdentifier {
		t.Errorf("expected metadata name %q, got %q", pluginIdentifier, reg.Metadata.Name)
	}
	if reg.Metadata.Version != pluginVersion {
		t.Errorf("expected metadata version %q, got %q", pluginVersion, reg.Metadata.Version)
	}
	if reg.SchemaVersion != pluginabi.SchemaVersion {
		t.Errorf("expected schema version %d, got %d", pluginabi.SchemaVersion, reg.SchemaVersion)
	}

	eng := currentEngine()
	if eng == nil {
		t.Fatal("engine should be initialized after register")
	}
	if eng.Config().AuthType != "Antigravity" {
		t.Errorf("expected engine auth-type 'Antigravity', got %q", eng.Config().AuthType)
	}
	if eng.Config().WindowHours != 12 {
		t.Errorf("expected engine window-hours 12, got %d", eng.Config().WindowHours)
	}

	// 2. Reconfigure call
	updatedConfigYAML := `
auth-type: "Gemini,Claude"
proxies:
  - "http://127.0.0.1:8080"
  - "socks5://127.0.0.1:1080"
failure-patterns:
  - "500"
window-hours: 48
`
	reconfReq, errMarshal := json.Marshal(lifecycleRequest{
		ConfigYAML:    []byte(updatedConfigYAML),
		SchemaVersion: pluginabi.SchemaVersion,
	})
	if errMarshal != nil {
		t.Fatalf("marshal reconfigure request: %v", errMarshal)
	}

	reconfRaw, errHandle := handleMethod(pluginabi.MethodPluginReconfigure, reconfReq)
	if errHandle != nil {
		t.Fatalf("handleMethod reconfigure: %v", errHandle)
	}
	var reconfEnv envelope
	if errUnmarshal := json.Unmarshal(reconfRaw, &reconfEnv); errUnmarshal != nil {
		t.Fatalf("unmarshal reconfigure envelope: %v", errUnmarshal)
	}
	if !reconfEnv.OK {
		t.Fatalf("reconfigure envelope not ok: %+v", reconfEnv.Error)
	}

	var reconfReg registration
	if errUnmarshal := json.Unmarshal(reconfEnv.Result, &reconfReg); errUnmarshal != nil {
		t.Fatalf("unmarshal reconfigure registration result: %v", errUnmarshal)
	}
	if !simulateHostValidPlugin(reconfReg.Metadata, reconfReg.Capabilities) {
		t.Fatalf("reconfigure result failed host validPlugin check: %+v", reconfReg)
	}
	if reconfReg.Metadata.Name != pluginIdentifier {
		t.Errorf("expected metadata name %q, got %q", pluginIdentifier, reconfReg.Metadata.Name)
	}
	if reconfReg.Metadata.Version != pluginVersion {
		t.Errorf("expected metadata version %q, got %q", pluginVersion, reconfReg.Metadata.Version)
	}

	eng = currentEngine()
	if eng == nil {
		t.Fatal("engine should remain active after reconfigure")
	}
	if eng.Config().AuthType != "Gemini,Claude" {
		t.Errorf("expected updated auth-type 'Gemini,Claude', got %q", eng.Config().AuthType)
	}
	if eng.Config().WindowHours != 48 {
		t.Errorf("expected updated window-hours 48, got %d", eng.Config().WindowHours)
	}
}

func TestParseLifecycleRequest(t *testing.T) {
	// Empty payload
	emptyReq, errEmpty := parseLifecycleRequest(nil)
	if errEmpty != nil {
		t.Fatalf("parse empty: %v", errEmpty)
	}
	if len(emptyReq.ConfigYAML) != 0 || emptyReq.SchemaVersion != 0 {
		t.Errorf("expected zero values for empty payload, got %+v", emptyReq)
	}

	// Standard bytes payload
	stdJSON := `{"config_yaml":"YXV0aC10eXBlOiAiQW50aWdyYXZpdHki","schema_version":5}`
	stdReq, errStd := parseLifecycleRequest([]byte(stdJSON))
	if errStd != nil {
		t.Fatalf("parse standard: %v", errStd)
	}
	if string(stdReq.ConfigYAML) != `auth-type: "Antigravity"` {
		t.Errorf("expected decoded config, got %q", string(stdReq.ConfigYAML))
	}
	if stdReq.SchemaVersion != 5 {
		t.Errorf("expected schema version 5, got %d", stdReq.SchemaVersion)
	}

	// Plain text fallback payload
	plainJSON := `{"config_yaml":"auth-type: Antigravity\n","schema_version":4}`
	plainReq, errPlain := parseLifecycleRequest([]byte(plainJSON))
	if errPlain != nil {
		t.Fatalf("parse plain text fallback: %v", errPlain)
	}
	if !strings.Contains(string(plainReq.ConfigYAML), "auth-type: Antigravity") {
		t.Errorf("expected plain text config, got %q", string(plainReq.ConfigYAML))
	}
	if plainReq.SchemaVersion != 4 {
		t.Errorf("expected schema version 4, got %d", plainReq.SchemaVersion)
	}
}

func TestQuiesceAndShutdown(t *testing.T) {
	setupTest(t)

	_, errRegister := handleMethod(pluginabi.MethodPluginRegister, nil)
	if errRegister != nil {
		t.Fatalf("register: %v", errRegister)
	}
	if currentEngine() == nil {
		t.Fatal("expected engine to be active")
	}

	// Quiesce
	quiesceRaw, errQuiesce := handleMethod(pluginabi.MethodPluginQuiesce, nil)
	if errQuiesce != nil {
		t.Fatalf("quiesce: %v", errQuiesce)
	}
	var qEnv envelope
	if errUnmarshal := json.Unmarshal(quiesceRaw, &qEnv); errUnmarshal != nil || !qEnv.OK {
		t.Fatalf("quiesce envelope not ok: %v", errUnmarshal)
	}
	if currentEngine() != nil {
		t.Fatal("expected engine to be nil after quiesce")
	}

	// Shutdown
	shutRaw, errShut := handleMethod(pluginabi.MethodPluginShutdown, nil)
	if errShut != nil {
		t.Fatalf("shutdown: %v", errShut)
	}
	var sEnv envelope
	if errUnmarshal := json.Unmarshal(shutRaw, &sEnv); errUnmarshal != nil || !sEnv.OK {
		t.Fatalf("shutdown envelope not ok: %v", errUnmarshal)
	}
}

func TestManagementAndUsage(t *testing.T) {
	setupTest(t)

	// Register with minimal config
	_, _ = handleMethod(pluginabi.MethodPluginRegister, nil)

	// Management register
	mRegRaw, errMReg := handleMethod(pluginabi.MethodManagementRegister, nil)
	if errMReg != nil {
		t.Fatalf("management register: %v", errMReg)
	}
	var mRegEnv envelope
	if errUnmarshal := json.Unmarshal(mRegRaw, &mRegEnv); errUnmarshal != nil || !mRegEnv.OK {
		t.Fatalf("management register envelope not ok: %v", errUnmarshal)
	}
	var mResp managementRegistrationResponse
	if errUnmarshal := json.Unmarshal(mRegEnv.Result, &mResp); errUnmarshal != nil {
		t.Fatalf("unmarshal management register result: %v", errUnmarshal)
	}
	if len(mResp.Routes) != 3 || len(mResp.Resources) != 2 {
		t.Fatalf("unexpected routes or resources: %+v", mResp)
	}

	testEndpoints := []struct {
		name       string
		path       string
		wantStatus int
		wantType   string
	}{
		{"mgmt status prefix", "/cpa-auto-proxy-test/status", 200, "application/json"},
		{"mgmt status v0", "/v0/management/cpa-auto-proxy-test/status", 200, "application/json"},
		{"mgmt status management prefix", "/management/cpa-auto-proxy-test/status", 200, "application/json"},
		{"resource status rejected", "/v0/resource/plugins/cpa-auto-proxy-test/status", 404, "application/json"},
		{"resource status plain rejected", "/resource/plugins/cpa-auto-proxy-test/status", 404, "application/json"},
		{"resource status uppercase rejected", "/v0/Resource/plugins/cpa-auto-proxy-test/status", 404, "application/json"},
		{"resource status multi-slash rejected", "//v0/resource/plugins/cpa-auto-proxy-test/status", 404, "application/json"},
		{"resource status dot-slash rejected", "/v0/resource/plugins/./cpa-auto-proxy-test/status", 404, "application/json"},
		{"mgmt export prefix", "/cpa-auto-proxy-test/export", 200, "application/json"},
		{"mgmt export v0", "/v0/management/cpa-auto-proxy-test/export", 200, "application/json"},
		{"mgmt export management prefix", "/management/cpa-auto-proxy-test/export", 200, "application/json"},
		{"resource export rejected", "/v0/resource/plugins/cpa-auto-proxy-test/export", 404, "application/json"},
		{"resource export plain rejected", "/resource/plugins/cpa-auto-proxy-test/export", 404, "application/json"},
		{"resource export uppercase rejected", "/V0/RESOURCE/plugins/cpa-auto-proxy-test/export", 404, "application/json"},
		{"resource export multi-slash rejected", "///v0/resource/plugins/cpa-auto-proxy-test/export", 404, "application/json"},
		{"non-mgmt arbitrary route rejected", "/other/prefix/status", 404, "application/json"},
		{"mgmt page prefix", "/cpa-auto-proxy-test/page", 200, "text/html"},
		{"mgmt page v0", "/v0/management/cpa-auto-proxy-test/page", 200, "text/html"},
		{"resource page", "/v0/resource/plugins/cpa-auto-proxy-test/page", 200, "text/html"},
		{"resource page uppercase", "/v0/Resource/plugins/cpa-auto-proxy-test/page", 200, "text/html"},
		{"resource page multi-slash", "//v0/resource/plugins/cpa-auto-proxy-test/page", 200, "text/html"},
		{"resource root", "/v0/resource/plugins/cpa-auto-proxy-test", 200, "text/html"},
		{"resource page query string", "/v0/resource/plugins/cpa-auto-proxy-test/page?t=123", 200, "text/html"},
		{"resource status query string rejected", "/v0/resource/plugins/cpa-auto-proxy-test/status?t=123", 404, "application/json"},
		{"unknown route", "/cpa-auto-proxy-test/not-exists", 404, ""},
	}

	for _, tc := range testEndpoints {
		t.Run(tc.name, func(t *testing.T) {
			reqBytes, _ := json.Marshal(map[string]any{
				"method": "GET",
				"path":   tc.path,
			})
			raw, errHandle := handleMethod(pluginabi.MethodManagementHandle, reqBytes)
			if errHandle != nil {
				t.Fatalf("handleMethod %s: %v", tc.name, errHandle)
			}
			var env envelope
			if errUnmarshal := json.Unmarshal(raw, &env); errUnmarshal != nil || !env.OK {
				t.Fatalf("handleMethod %s envelope not ok: %v", tc.name, errUnmarshal)
			}
			var resp plugin.ManagementResponse
			if errUnmarshal := json.Unmarshal(env.Result, &resp); errUnmarshal != nil {
				t.Fatalf("unmarshal response %s: %v", tc.name, errUnmarshal)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("StatusCode = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if tc.wantType != "" {
				ct := resp.Headers["Content-Type"]
				if len(ct) == 0 || !strings.Contains(ct[0], tc.wantType) {
					t.Fatalf("Content-Type = %v, want substring %q", ct, tc.wantType)
				}
			}
		})
	}
	// Usage handle
	usageReq, _ := json.Marshal(pluginapi.UsageRecord{
		Provider: "Antigravity",
		Failed:   false,
	})
	usageRaw, errUsage := handleMethod(pluginabi.MethodUsageHandle, usageReq)
	if errUsage != nil {
		t.Fatalf("usage handle: %v", errUsage)
	}
	var usageEnv envelope
	if errUnmarshal := json.Unmarshal(usageRaw, &usageEnv); errUnmarshal != nil || !usageEnv.OK {
		t.Fatalf("usage handle envelope not ok: %v", errUnmarshal)
	}
}
func TestStaticPageWithoutEngineBranching(t *testing.T) {
	setupTest(t)
	// Ensure engine is nil
	shutdownEngine()

	// 1. Static page via resource route without engine must return 200 StatusPageHTML
	reqPageResource, _ := json.Marshal(map[string]any{
		"method": "GET",
		"path":   "/v0/resource/plugins/cpa-auto-proxy-test/page",
	})
	rawPage, errPage := handleMethod(pluginabi.MethodManagementHandle, reqPageResource)
	if errPage != nil {
		t.Fatalf("handleMethod page: %v", errPage)
	}
	var envPage envelope
	if err := json.Unmarshal(rawPage, &envPage); err != nil || !envPage.OK {
		t.Fatalf("page envelope not ok: %v", err)
	}
	var respPage plugin.ManagementResponse
	if err := json.Unmarshal(envPage.Result, &respPage); err != nil {
		t.Fatalf("unmarshal page resp: %v", err)
	}
	if respPage.StatusCode != 200 {
		t.Fatalf("page status = %d, want 200", respPage.StatusCode)
	}
	if string(respPage.Body) != plugin.StatusPageHTML() {
		t.Fatalf("page body does not match static StatusPageHTML")
	}

	// 2. Resource status must return 404, not 503, preserving boundary
	reqResStatus, _ := json.Marshal(map[string]any{
		"method": "GET",
		"path":   "/v0/resource/plugins/cpa-auto-proxy-test/status",
	})
	rawResStatus, _ := handleMethod(pluginabi.MethodManagementHandle, reqResStatus)
	var envResStatus envelope
	_ = json.Unmarshal(rawResStatus, &envResStatus)
	var respResStatus plugin.ManagementResponse
	_ = json.Unmarshal(envResStatus.Result, &respResStatus)
	if respResStatus.StatusCode != 404 {
		t.Fatalf("resource status without engine = %d, want 404", respResStatus.StatusCode)
	}

	// 3. Mgmt status returns 503 plugin not configured
	reqMgmtStatus, _ := json.Marshal(map[string]any{
		"method": "GET",
		"path":   "/v0/management/cpa-auto-proxy-test/status",
	})
	rawMgmtStatus, _ := handleMethod(pluginabi.MethodManagementHandle, reqMgmtStatus)
	var envMgmtStatus envelope
	_ = json.Unmarshal(rawMgmtStatus, &envMgmtStatus)
	var respMgmtStatus plugin.ManagementResponse
	_ = json.Unmarshal(envMgmtStatus.Result, &respMgmtStatus)
	if respMgmtStatus.StatusCode != 503 {
		t.Fatalf("mgmt status without engine = %d, want 503", respMgmtStatus.StatusCode)
	}
}

func TestCleanRoutePathAndExportLimit(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/v0/resource/plugins/cpa-auto-proxy-test/page", "/v0/resource/plugins/cpa-auto-proxy-test/page"},
		{"/v0/resource/plugins/./cpa-auto-proxy-test/page", "/v0/resource/plugins/cpa-auto-proxy-test/page"},
		{"//v0//resource//plugins//page", "/v0/resource/plugins/page"},
		{"/v0/resource/plugins/page?foo=bar", "/v0/resource/plugins/page"},
		{"/v0/resource/plugins/page#section", "/v0/resource/plugins/page"},
		{"resource/page", "/resource/page"},
	}
	for _, tc := range cases {
		got := cleanRoutePath(tc.input)
		if got != tc.want {
			t.Errorf("cleanRoutePath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}

	limitCases := []struct {
		query map[string][]string
		want  int
	}{
		{nil, exportLimitDefault},
		{map[string][]string{"limit": {"10"}}, 10},
		{map[string][]string{"limit": {"9999"}}, exportLimitMax},
		{map[string][]string{"limit": {"-5"}}, exportLimitDefault},
		{map[string][]string{"limit": {"abc"}}, exportLimitDefault},
		{map[string][]string{"limit": {"", "25"}}, 25},
	}
	for _, tc := range limitCases {
		got := exportLimit(tc.query)
		if got != tc.want {
			t.Errorf("exportLimit(%v) = %d, want %d", tc.query, got, tc.want)
		}
	}
}
