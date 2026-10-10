// Package matcher compiles the configured failure response patterns and
// decides whether an upstream failure matches one of them.
package matcher

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Rule is one compiled failure pattern: either a status-code rule or a
// regular expression matched against the failure body.
type Rule struct {
	// StatusCode is the exact HTTP status code when greater than zero.
	StatusCode int
	// Body matches the failure response body when non-nil.
	Body *regexp.Regexp
	// Raw preserves the original configured pattern.
	Raw string
}

// Matcher holds the compiled failure rules.
type Matcher struct {
	rules []Rule
}

// New compiles the configured patterns. A pattern consisting only of decimal
// digits is treated as a status code; anything else is compiled as a regular
// expression.
func New(patterns []string) (*Matcher, error) {
	m := &Matcher{rules: make([]Rule, 0, len(patterns))}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if code, err := strconv.Atoi(pattern); err == nil {
			m.rules = append(m.rules, Rule{StatusCode: code, Raw: pattern})
			continue
		}
		body, errCompile := regexp.Compile(pattern)
		if errCompile != nil {
			return nil, fmt.Errorf("compile failure pattern %q: %w", pattern, errCompile)
		}
		m.rules = append(m.rules, Rule{Body: body, Raw: pattern})
	}
	return m, nil
}

// Empty reports whether no failure rules are configured.
func (m *Matcher) Empty() bool {
	return m == nil || len(m.rules) == 0
}

// Match reports whether the failure status code or body matches any rule.
// When no rules are configured (Empty() == true), it matches every failure.
func (m *Matcher) Match(statusCode int, body string) bool {
	if m.Empty() {
		return true
	}
	for _, rule := range m.rules {
		if rule.StatusCode > 0 {
			if rule.StatusCode == statusCode {
				return true
			}
			continue
		}
		if rule.Body != nil && rule.Body.MatchString(body) {
			return true
		}
	}
	return false
}

// Rules returns the compiled rules.
func (m *Matcher) Rules() []Rule {
	if m == nil {
		return nil
	}
	return append([]Rule(nil), m.rules...)
}
