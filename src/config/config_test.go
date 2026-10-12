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
	if len(cfg.Proxies) != 0 || len(cfg.FailurePatterns) != 0 {
		t.Fatalf("expected empty proxies and patterns, got %v / %v", cfg.Proxies, cfg.FailurePatterns)
	}
}

func TestParseEmptyAuthType(t *testing.T) {
	raw := []byte(`
auth-type: ""
window-hours: 12
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

func TestConfigAuthTypes(t *testing.T) {
	tests := []struct {
		name     string
		authType string
		want     []string
	}{
		{name: "empty", authType: "", want: nil},
		{name: "whitespace only", authType: "   ", want: nil},
		{name: "commas only", authType: " , , , ", want: nil},
		{name: "single", authType: "Antigravity", want: []string{"Antigravity"}},
		{name: "multiple with whitespace", authType: " Antigravity , gemini , , claude ", want: []string{"Antigravity", "gemini", "claude"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{AuthType: tc.authType}
			got := cfg.AuthTypes()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("AuthTypes() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfigMatchesAuthType(t *testing.T) {
	t.Run("empty config matches nothing", func(t *testing.T) {
		cfg := Config{}
		if cfg.MatchesAuthType("Antigravity") {
			t.Fatal("expected empty config to match nothing")
		}
		if cfg.MatchesAuthType("") {
			t.Fatal("expected empty target to return false")
		}
	})

	t.Run("single auth type", func(t *testing.T) {
		cfg := Config{AuthType: "Antigravity"}
		if !cfg.MatchesAuthType("Antigravity") {
			t.Fatal("expected exact match to succeed")
		}
		if !cfg.MatchesAuthType("antigravity") {
			t.Fatal("expected lowercase match to succeed")
		}
		if !cfg.MatchesAuthType("ANTIGRAVITY") {
			t.Fatal("expected uppercase match to succeed")
		}
		if !cfg.MatchesAuthType("  antigravity  ") {
			t.Fatal("expected trimmed match to succeed")
		}
		if cfg.MatchesAuthType("gemini") {
			t.Fatal("expected non-matching target to return false")
		}
		if cfg.MatchesAuthType("") || cfg.MatchesAuthType("   ") {
			t.Fatal("expected empty/whitespace target to return false")
		}
	})

	t.Run("comma-separated auth types", func(t *testing.T) {
		cfg := Config{AuthType: " Antigravity , Gemini "}
		if !cfg.MatchesAuthType("antigravity") {
			t.Fatal("expected antigravity to match")
		}
		if !cfg.MatchesAuthType("GEMINI") {
			t.Fatal("expected GEMINI to match")
		}
		if cfg.MatchesAuthType("claude") {
			t.Fatal("expected claude to not match")
		}
	})
}

func TestParseInvalidYAML(t *testing.T) {
	_, err := Parse([]byte("auth-type: [broken"))
	if err == nil {
		t.Fatal("Parse expected error for invalid YAML, got nil")
	}
	if !strings.Contains(err.Error(), "decode cpa-auto-proxy-test config") {
		t.Fatalf("expected error containing 'decode cpa-auto-proxy-test config', got %v", err)
	}
}
