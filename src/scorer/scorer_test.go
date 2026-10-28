package scorer

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestScoreSmoothing(t *testing.T) {
	cases := []struct {
		name  string
		stats Stats
		want  float64
	}{
		{"unseen proxy", Stats{}, 0.5},
		{"all success long history empty recent", Stats{RecentSuccess: 0, RecentTotal: 0, LongSuccess: 100, LongTotal: 100}, 101.0 / 102.0},
		{"all failures", Stats{RecentSuccess: 0, RecentTotal: 10, LongSuccess: 0, LongTotal: 10}, 1.0 / 12.0},
		{"recent dominates", Stats{RecentSuccess: 9, RecentTotal: 10, LongSuccess: 0, LongTotal: 100}, RecentWeight*(10.0/12.0) + LongTermWeight*(1.0/102.0)},
	}
	for _, tc := range cases {
		got := Score(tc.stats)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("%s: Score = %g, want %g", tc.name, got, tc.want)
		}
	}
	if Score(Stats{RecentSuccess: 9, RecentTotal: 10}) <= Score(Stats{RecentSuccess: 1, RecentTotal: 10}) {
		t.Fatal("higher recent success rate must score higher")
	}
}

func TestScoreConfidenceScaling(t *testing.T) {
	// With 1 recent observation (conf = 0.2), recent weight is 0.7 * 0.2 = 0.14, long weight 0.86
	stats1 := Stats{RecentSuccess: 0, RecentTotal: 1, LongSuccess: 90, LongTotal: 100}
	score1 := Score(stats1)

	// With 5 recent observations (conf = 1.0), recent weight is 0.7, long weight 0.3
	stats5 := Stats{RecentSuccess: 0, RecentTotal: 5, LongSuccess: 90, LongTotal: 100}
	score5 := Score(stats5)

	// A single recent failure should not crash the score as hard as 5 consecutive failures
	if score1 <= score5 {
		t.Fatalf("score with 1 recent failure (%g) should be higher than with 5 recent failures (%g)", score1, score5)
	}
}

func TestScoreWithEmpiricalPrior(t *testing.T) {
	unseen := Stats{}
	scoreHighPrior := ScoreWithPrior(unseen, 0.8, DefaultMinRecentSamples)
	scoreLowPrior := ScoreWithPrior(unseen, 0.1, DefaultMinRecentSamples)

	if scoreHighPrior <= scoreLowPrior {
		t.Fatalf("unseen proxy with higher prior (%g) should score higher than low prior (%g)", scoreHighPrior, scoreLowPrior)
	}
}

func TestSelectPrefersUntriedInPriorOrder(t *testing.T) {
	proxies := []string{"http://a", "http://b", "http://c"}
	untried := map[string]bool{"http://a": true, "http://b": true, "http://c": true}

	// Without priors, fall back to config order (http://a)
	got := Select(proxies, untried, "http://a", map[string]Stats{})
	if got != "http://a" {
		t.Fatalf("Select without priors = %q, want http://a", got)
	}

	// With priors, choose http://c which has the highest prior
	priors := map[string]float64{
		"http://a": 0.2,
		"http://b": 0.4,
		"http://c": 0.8,
	}
	got = SelectCandidate(SelectOptions{
		Candidates:   proxies,
		Untried:      untried,
		GlobalPriors: priors,
	})
	if got != "http://c" {
		t.Fatalf("SelectCandidate with priors = %q, want http://c", got)
	}
}

func TestSelectExcludesFailingProxy(t *testing.T) {
	proxies := []string{"http://best", "http://alt"}
	stats := map[string]Stats{
		"http://best": {RecentSuccess: 95, RecentTotal: 100, LongSuccess: 950, LongTotal: 1000},
		"http://alt":  {RecentSuccess: 4, RecentTotal: 10, LongSuccess: 40, LongTotal: 100},
	}

	// Normal select picks http://best
	got := SelectCandidate(SelectOptions{
		Candidates: proxies,
		Stats:      stats,
		Current:    "http://best",
	})
	if got != "http://best" {
		t.Fatalf("SelectCandidate normal = %q, want http://best", got)
	}

	// When http://best fails, Exclude prevents self-selection deadlock
	got = SelectCandidate(SelectOptions{
		Candidates: proxies,
		Stats:      stats,
		Current:    "http://best",
		Exclude:    "http://best",
	})
	if got != "http://alt" {
		t.Fatalf("SelectCandidate with exclude = %q, want http://alt", got)
	}
}

func TestSelectCooldownPreference(t *testing.T) {
	now := time.Now()
	proxies := []string{"http://cooling", "http://active"}
	stats := map[string]Stats{
		"http://cooling": {RecentSuccess: 9, RecentTotal: 10, LongSuccess: 90, LongTotal: 100},
		"http://active":  {RecentSuccess: 5, RecentTotal: 10, LongSuccess: 50, LongTotal: 100},
	}
	cooldowns := map[string]time.Time{
		"http://cooling": now.Add(30 * time.Second),
	}

	// http://cooling has higher score, but is cooling down -> choose active
	got := SelectCandidate(SelectOptions{
		Candidates: proxies,
		Stats:      stats,
		Cooldowns:  cooldowns,
		Now:        now,
	})
	if got != "http://active" {
		t.Fatalf("SelectCandidate should prefer active proxy over cooling proxy, got %q", got)
	}

	// When cooldown expires, http://cooling becomes eligible again
	got = SelectCandidate(SelectOptions{
		Candidates: proxies,
		Stats:      stats,
		Cooldowns:  cooldowns,
		Now:        now.Add(31 * time.Second),
	})
	if got != "http://cooling" {
		t.Fatalf("SelectCandidate should pick http://cooling after cooldown expires, got %q", got)
	}
}

func TestSelectTieKeepsCurrent(t *testing.T) {
	proxies := []string{"http://a", "http://b"}
	stats := map[string]Stats{
		"http://a": {RecentSuccess: 5, RecentTotal: 10},
		"http://b": {RecentSuccess: 5, RecentTotal: 10},
	}
	got := Select(proxies, map[string]bool{}, "http://b", stats)
	if got != "http://b" {
		t.Fatalf("Select = %q, want current proxy kept on tie", got)
	}
	got = Select(proxies, map[string]bool{}, "default", stats)
	if got != "http://a" {
		t.Fatalf("Select = %q, want config order winner when current is pseudo proxy", got)
	}
}

func TestSelectNeverReturnsUnknownOrEmpty(t *testing.T) {
	proxies := []string{"http://a"}
	got := Select(proxies, map[string]bool{}, "http://stale", map[string]Stats{
		"http://stale": {RecentSuccess: 100, RecentTotal: 100},
	})
	if got != "http://a" {
		t.Fatalf("Select = %q, want configured proxy only", got)
	}
	if Select(nil, map[string]bool{}, "", nil) != "" {
		t.Fatal("Select without candidates must return empty string")
	}
}

func TestRankedStableOrder(t *testing.T) {
	proxies := []string{"http://a", "http://b", "http://c"}
	stats := map[string]Stats{
		"http://a": {RecentSuccess: 5, RecentTotal: 10},
		"http://b": {RecentSuccess: 9, RecentTotal: 10},
	}
	ranked := Ranked(proxies, stats)
	if fmt.Sprint(ranked) != "[http://b http://a http://c]" {
		t.Fatalf("Ranked = %v, want b, a, c", ranked)
	}
}

func TestSelectAllCoolingPrefersShortestCooldown(t *testing.T) {
	now := time.Now()
	proxies := []string{"http://long-cool", "http://short-cool-low", "http://short-cool-high"}
	stats := map[string]Stats{
		"http://long-cool":       {RecentSuccess: 9, RecentTotal: 10, LongSuccess: 95, LongTotal: 100},
		"http://short-cool-low":  {RecentSuccess: 4, RecentTotal: 10, LongSuccess: 40, LongTotal: 100},
		"http://short-cool-high": {RecentSuccess: 8, RecentTotal: 10, LongSuccess: 80, LongTotal: 100},
	}
	cooldowns := map[string]time.Time{
		"http://long-cool":       now.Add(10 * time.Minute),
		"http://short-cool-low":  now.Add(15 * time.Second),
		"http://short-cool-high": now.Add(15 * time.Second),
	}

	got := SelectCandidate(SelectOptions{
		Candidates: proxies,
		Stats:      stats,
		Cooldowns:  cooldowns,
		Now:        now,
	})
	if got != "http://short-cool-high" {
		t.Fatalf("SelectCandidate with all cooling = %q, want http://short-cool-high", got)
	}
}
