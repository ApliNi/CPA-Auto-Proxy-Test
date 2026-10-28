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
	if len(cfg.Proxies) != 0 || len(cfg.FailurePatterns) != 0 {
		t.Fatalf("expected empty proxies and patterns, got %v / %v", cfg.Proxies, cfg.FailurePatterns)
	}
}

func TestParseEmptyAuthType(t *testing.T) {
	raw := []byte(`
window-hours: 12
min-recent-samples: 10
failure-cooldown-sec: 30
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
}

func TestConfigAuthTypes(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"   ", nil},
		{",,,", nil},
		{"antigravity", []string{"antigravity"}},
		{"Antigravity, gemini, , CLAUDE", []string{"Antigravity", "gemini", "CLAUDE"}},
	}
	for _, tc := range cases {
		c := Config{AuthType: tc.input}
		got := c.AuthTypes()
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("AuthTypes(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestConfigMatchesAuthType(t *testing.T) {
	cfg := Config{AuthType: "Antigravity, gemini"}
	cases := []struct {
		target string
		want   bool
	}{
		{"", false},
		{"   ", false},
		{"antigravity", true},
		{"Antigravity", true},
		{"ANTIGRAVITY", true},
		{"gemini", true},
		{"GEMINI", true},
		{"claude", false},
		{"anti", false},
	}
	for _, tc := range cases {
		if got := cfg.MatchesAuthType(tc.target); got != tc.want {
			t.Fatalf("MatchesAuthType(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

func TestParseInvalidYAML(t *testing.T) {
	raw := []byte("invalid: yaml: :")
	_, err := Parse(raw)
	if err == nil {
		t.Fatal("expected error on invalid YAML, got nil")
	}
	if !strings.Contains(err.Error(), "decode cpa-auto-proxy-test config") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
