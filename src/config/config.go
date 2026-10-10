// Package config parses the CPA-Auto-Proxy-Test plugin configuration from the raw YAML
// subtree delivered through plugin.register / plugin.reconfigure.
package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the CPA-Auto-Proxy-Test plugin settings.
type Config struct {
	// AuthType is the credential type managed by this plugin (case-insensitive).
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
		AuthType:    "Antigravity",
		WindowHours: 24,
	}
}

// Parse decodes the raw YAML configuration on top of the defaults and
// normalizes every field. Empty or nil input yields the defaults.
func Parse(raw []byte) (Config, error) {
	cfg := Default()
	if len(raw) > 0 {
		if errUnmarshal := yaml.Unmarshal(raw, &cfg); errUnmarshal != nil {
			return Config{}, fmt.Errorf("decode CPA-Auto-Proxy-Test config: %w", errUnmarshal)
		}
	}
	cfg.AuthType = strings.TrimSpace(cfg.AuthType)
	if cfg.AuthType == "" {
		cfg.AuthType = "Antigravity"
	}
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
