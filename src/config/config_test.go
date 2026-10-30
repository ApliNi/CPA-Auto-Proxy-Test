package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse(nil) error = %v", err)
	}
	if cfg.AuthType != "" {
		t.Fatalf("AuthType = %q, want empty", cfg.AuthType)
	}
	if cfg.WindowHours != 24 {
		t.Fatalf("WindowHours = %d, want 24", cfg.WindowHours)
	}
	if cfg.MinRecentSamples != 5 {
		t.Fatalf("MinRecentSamples = %d, want 5", cfg.MinRecentSamples)
	}
	if cfg.FailureCooldownSec != 15 {
		t.Fatalf("FailureCooldownSec = %d, want 15", cfg.FailureCooldownSec)
	}
	if cfg.SwitchDebounceSec != 10 {
		t.Fatalf("SwitchDebounceSec = %d, want 10", cfg.SwitchDebounceSec)
	}
	if len(cfg.Proxies) != 0 || len(cfg.FailurePatterns) != 0 {
		t.Fatalf("expected empty proxies and patterns, got %v / %v", cfg.Proxies, cfg.FailurePatterns)
	}
}

func TestParseEmptyAuthType(t *testing.T) {
	raw := []byte(`
auth-type: ""
window-hours: 12
min-recent-samples: 10
failure-cooldown-sec: 30
switch-debounce-sec: 5
`)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if cfg.AuthType != "" {
		t.Fatalf("AuthType = %q, want empty string", cfg.AuthType)
	}
	if cfg.WindowHours != 12 {
		t.Fatalf("WindowHours = %d, want 12", cfg.WindowHours)
	}
	if cfg.MinRecentSamples != 10 {
		t.Fatalf("MinRecentSamples = %d, want 10", cfg.MinRecentSamples)
	}
	if cfg.FailureCooldownSec != 30 {
		t.Fatalf("FailureCooldownSec = %d, want 30", cfg.FailureCooldownSec)
	}
	if cfg.SwitchDebounceSec != 5 {
		t.Fatalf("SwitchDebounceSec = %d, want 5", cfg.SwitchDebounceSec)
	}
}

func TestParseOverridesAndNormalization(t *testing.T) {
	raw := []byte(`
auth-type: "  Antigravity, gemini ,  "
proxies:
  - " http://proxy1.example.com "
  - "http://proxy2.example.com"
  - "http://proxy1.example.com"
  - ""
failure-patterns:
  - " 400 "
  - ""
  - "User location is not supported"
window-hours: -5
min-recent-samples: -1
failure-cooldown-sec: 0
switch-debounce-sec: -3
`)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if cfg.AuthType != "Antigravity, gemini ," {
		t.Fatalf("AuthType = %q", cfg.AuthType)
	}
	wantProxies := []string{"http://proxy1.example.com", "http://proxy2.example.com"}
	if !reflect.DeepEqual(cfg.Proxies, wantProxies) {
		t.Fatalf("Proxies = %v, want %v", cfg.Proxies, wantProxies)
	}
	wantPatterns := []string{"400", "User location is not supported"}
	if !reflect.DeepEqual(cfg.FailurePatterns, wantPatterns) {
		t.Fatalf("FailurePatterns = %v, want %v", cfg.FailurePatterns, wantPatterns)
	}
	if cfg.WindowHours != 24 {
		t.Fatalf("WindowHours should reset to default 24 when non-positive, got %d", cfg.WindowHours)
	}
	if cfg.MinRecentSamples != 5 {
		t.Fatalf("MinRecentSamples should reset to default 5 when non-positive, got %d", cfg.MinRecentSamples)
	}
	if cfg.FailureCooldownSec != 15 {
		t.Fatalf("FailureCooldownSec should reset to default 15 when non-positive, got %d", cfg.FailureCooldownSec)
	}
	if cfg.SwitchDebounceSec != 10 {
		t.Fatalf("SwitchDebounceSec should reset to default 10 when negative, got %d", cfg.SwitchDebounceSec)
	}
}

func TestParseZeroSwitchDebounceDisablesDebounce(t *testing.T) {
	raw := []byte(`
switch-debounce-sec: 0
`)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if cfg.SwitchDebounceSec != 0 {
		t.Fatalf("SwitchDebounceSec = %d, want 0 when explicitly disabled", cfg.SwitchDebounceSec)
	}
}

func TestConfigAuthTypes(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"   ", nil},
		{"antigravity", []string{"antigravity"}},
		{"Antigravity, Gemini", []string{"Antigravity", "Gemini"}},
		{"  antigravity ,  , gemini  ", []string{"antigravity", "gemini"}},
		{",,,", nil},
	}
	for _, tc := range cases {
		cfg := Config{AuthType: tc.input}
		got := cfg.AuthTypes()
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("AuthTypes(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestConfigMatchesAuthType(t *testing.T) {
	cfg := Config{AuthType: "Antigravity, Gemini"}
	if !cfg.MatchesAuthType("antigravity") {
		t.Error("expected matches antigravity")
	}
	if !cfg.MatchesAuthType("GEMINI") {
		t.Error("expected matches GEMINI")
	}
	if cfg.MatchesAuthType("claude") {
		t.Error("unexpected match claude")
	}
	if cfg.MatchesAuthType("") {
		t.Error("unexpected match empty string")
	}

	emptyCfg := Config{}
	if emptyCfg.MatchesAuthType("antigravity") {
		t.Error("expected no match when AuthType is empty")
	}
}

func TestParseInvalidYAML(t *testing.T) {
	_, err := Parse([]byte(": invalid yaml :"))
	if err == nil {
		t.Fatal("expected error parsing invalid YAML")
	}
	if !strings.Contains(err.Error(), "decode cpa-auto-proxy-test config") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
