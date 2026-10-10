package scorer

import (
	"fmt"
	"math"
	"testing"
)

func TestScoreSmoothing(t *testing.T) {
	cases := []struct {
		name  string
		stats Stats
		want  float64
	}{
		{"unseen proxy", Stats{}, 0.5},
		{"all success long history", Stats{RecentSuccess: 0, RecentTotal: 0, LongSuccess: 100, LongTotal: 100}, RecentWeight*0.5 + LongTermWeight*(101.0/102.0)},
		{"all failures", Stats{RecentSuccess: 0, RecentTotal: 10, LongSuccess: 0, LongTotal: 10}, RecentWeight*(1.0/12.0) + LongTermWeight*(1.0/12.0)},
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

func TestSelectPrefersUntriedInConfigOrder(t *testing.T) {
	proxies := []string{"http://a", "http://b", "http://c"}
	got := Select(proxies, map[string]bool{"http://a": false, "http://b": true, "http://c": true}, "http://a", map[string]Stats{})
	if got != "http://b" {
		t.Fatalf("Select = %q, want first untried in config order", got)
	}
}

func TestSelectFallsBackToBestScore(t *testing.T) {
	proxies := []string{"http://a", "http://b"}
	stats := map[string]Stats{
		"http://a": {RecentSuccess: 1, RecentTotal: 10, LongSuccess: 1, LongTotal: 10},
		"http://b": {RecentSuccess: 9, RecentTotal: 10, LongSuccess: 90, LongTotal: 100},
	}
	got := Select(proxies, map[string]bool{}, "http://a", stats)
	if got != "http://b" {
		t.Fatalf("Select = %q, want highest scored proxy", got)
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
