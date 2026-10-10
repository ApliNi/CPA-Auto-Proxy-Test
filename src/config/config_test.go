package config

import (
	"testing"
)

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse(nil) error = %v", err)
	}
	if cfg.AuthType != "Antigravity" {
		t.Fatalf("AuthType = %q, want Antigravity", cfg.AuthType)
	}
	if cfg.WindowHours != 24 {
		t.Fatalf("WindowHours = %d, want 24", cfg.WindowHours)
	}
	if len(cfg.Proxies) != 0 || len(cfg.FailurePatterns) != 0 {
		t.Fatalf("expected empty proxies and patterns, got %v / %v", cfg.Proxies, cfg.FailurePatterns)
	}
}

func TestParseOverridesAndNormalization(t *testing.T) {
	raw := []byte(`
auth-type: " antigravity "
proxies:
  - "http://proxy-a.example:8080"
  - "http://proxy-a.example:8080"
  - "  "
  - "socks5://proxy-b.example:1080"
failure-patterns:
  - "400"
  - "User location is not supported"
  - ""
window-hours: 0
`)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if cfg.AuthType != "antigravity" {
		t.Fatalf("AuthType = %q, want trimmed value", cfg.AuthType)
	}
	if len(cfg.Proxies) != 2 {
		t.Fatalf("Proxies = %v, want deduplicated and trimmed", cfg.Proxies)
	}
	if cfg.Proxies[0] != "http://proxy-a.example:8080" || cfg.Proxies[1] != "socks5://proxy-b.example:1080" {
		t.Fatalf("Proxies = %v, unexpected entries", cfg.Proxies)
	}
	if len(cfg.FailurePatterns) != 2 {
		t.Fatalf("FailurePatterns = %v, want blank entries dropped", cfg.FailurePatterns)
	}
	if cfg.WindowHours != 24 {
		t.Fatalf("WindowHours = %d, want default 24 for invalid input", cfg.WindowHours)
	}
}

func TestParseInvalidYAML(t *testing.T) {
	if _, err := Parse([]byte("auth-type: [broken")); err == nil {
		t.Fatal("Parse expected error for invalid YAML, got nil")
	}
}

