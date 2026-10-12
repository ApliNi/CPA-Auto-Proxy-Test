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
	if len(mResp.Routes) != 3 || len(mResp.Resources) != 6 {
		t.Fatalf("unexpected routes or resources: %+v", mResp)
	}

	testEndpoints := []struct {
		name       string
		path       string
		wantStatus int
		wantType   string
	}{
		{"mgmt status", "/cpa-auto-proxy-test/status", 200, "application/json"},
		{"resource status", "/v0/resource/plugins/cpa-auto-proxy-test/status", 200, "application/json"},
		{"mgmt export", "/cpa-auto-proxy-test/export", 200, "application/json"},
		{"resource export", "/v0/resource/plugins/cpa-auto-proxy-test/export", 200, "application/json"},
		{"mgmt page", "/cpa-auto-proxy-test/page", 200, "text/html"},
		{"resource page", "/v0/resource/plugins/cpa-auto-proxy-test/page", 200, "text/html"},
		{"resource root", "/v0/resource/plugins/cpa-auto-proxy-test", 200, "text/html"},
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
