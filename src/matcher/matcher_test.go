package matcher

import "testing"

func TestNewStatusAndBodyRules(t *testing.T) {
	m, err := New([]string{"429", "User location is not supported", `^.*FAILED_PRECONDITION.*$`, "  "})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	rules := m.Rules()
	if len(rules) != 3 {
		t.Fatalf("rules = %d, want 3 (blank dropped)", len(rules))
	}
	if rules[0].StatusCode != 429 {
		t.Fatalf("rules[0].StatusCode = %d, want 429", rules[0].StatusCode)
	}
	if rules[1].Body == nil || !rules[1].Body.MatchString("... User location is not supported for the API use.") {
		t.Fatalf("rules[1] should match the location error body")
	}
	if rules[2].Body == nil || !rules[2].Body.MatchString(`{"error":{"status":"FAILED_PRECONDITION"}}`) {
		t.Fatalf("rules[2] should match FAILED_PRECONDITION body")
	}
}

func TestNewRejectsInvalidRegex(t *testing.T) {
	if _, err := New([]string{"400", "[unclosed"}); err == nil {
		t.Fatal("New expected compile error for invalid regex, got nil")
	}
}

func TestMatchStatusCodeAndBody(t *testing.T) {
	m, err := New([]string{"400", "User location is not supported"})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	cases := []struct {
		name string
		code int
		body string
		want bool
	}{
		{"status match", 400, "anything", true},
		{"status mismatch", 500, "anything", false},
		{"body match", 200, `{"message":"User location is not supported for the API use."}`, true},
		{"body mismatch", 200, "all good", false},
		{"case sensitive", 200, "user location is not supported", false},
	}
	for _, tc := range cases {
		if got := m.Match(tc.code, tc.body); got != tc.want {
			t.Fatalf("%s: Match(%d, %q) = %v, want %v", tc.name, tc.code, tc.body, got, tc.want)
		}
	}
}

func TestEmptyMatcherMatchesAllFailures(t *testing.T) {
	m, err := New(nil)
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if !m.Empty() {
		t.Fatal("Empty() = false for nil patterns")
	}
	if !m.Match(500, "any failure") {
		t.Fatal("empty matcher must match all failures")
	}
	if !m.Match(400, "") {
		t.Fatal("empty matcher must match 400 failure")
	}
	var nilMatcher *Matcher
	if !nilMatcher.Empty() || !nilMatcher.Match(500, "x") {
		t.Fatal("nil matcher must be empty and match all failures")
	}
}
