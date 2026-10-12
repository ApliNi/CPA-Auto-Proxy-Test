// Package config parses the cpa-auto-proxy-test plugin configuration from the raw YAML
// subtree delivered through plugin.register / plugin.reconfigure.
package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the cpa-auto-proxy-test plugin settings.
type Config struct {
	// AuthType is the credential type (or comma-separated list of types) managed by this plugin (case-insensitive).
	// If empty, no credentials are managed.
	AuthType string `yaml:"auth-type"`
	// Proxies lists candidate proxy URLs, one full URL per entry.
	Proxies []string `yaml:"proxies"`
	// FailurePatterns lists failure response matchers: each entry is either a
	// regular expression matched against Failure.Body, or a plain numeric
	// status code matched against Failure.StatusCode.
	FailurePatterns []string `yaml:"failure-patterns"`
	// WindowHours is the length of the recent observation window in hours.
	WindowHours int `yaml:"window-hours"`
}

// Default returns the built-in default configuration.
func Default() Config {
	return Config{
		AuthType:    "",
		WindowHours: 24,
	}
}

// AuthTypes returns the trimmed, non-empty credential types configured in AuthType.
// Returns nil if no credential types are configured.
func (c Config) AuthTypes() []string {
	if strings.TrimSpace(c.AuthType) == "" {
		return nil
	}
	parts := strings.Split(c.AuthType, ",")
	var types []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			types = append(types, p)
		}
	}
	if len(types) == 0 {
		return nil
	}
	return types
}

// MatchesAuthType reports whether target matches any configured AuthType (case-insensitive).
// Returns false if target is empty or no AuthType is configured.
func (c Config) MatchesAuthType(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, authType := range c.AuthTypes() {
		if strings.EqualFold(target, authType) {
			return true
		}
	}
	return false
}

// Parse decodes the raw YAML configuration on top of the defaults and
// normalizes every field. Empty or nil input yields the defaults.
func Parse(raw []byte) (Config, error) {
	cfg := Default()
	if len(raw) > 0 {
		if errUnmarshal := yaml.Unmarshal(raw, &cfg); errUnmarshal != nil {
			return Config{}, fmt.Errorf("decode cpa-auto-proxy-test config: %w", errUnmarshal)
		}
	}
	cfg.AuthType = strings.TrimSpace(cfg.AuthType)
	proxies := make([]string, 0, len(cfg.Proxies))
	seen := make(map[string]struct{}, len(cfg.Proxies))
	for _, proxy := range cfg.Proxies {
		proxy = strings.TrimSpace(proxy)
		if proxy == "" {
			continue
		}
		if _, ok := seen[proxy]; ok {
			continue
		}
		seen[proxy] = struct{}{}
		proxies = append(proxies, proxy)
	}
	cfg.Proxies = proxies
	patterns := make([]string, 0, len(cfg.FailurePatterns))
	for _, pattern := range cfg.FailurePatterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		patterns = append(patterns, pattern)
	}
	cfg.FailurePatterns = patterns
	if cfg.WindowHours <= 0 {
		cfg.WindowHours = 24
	}
	return cfg, nil
}
