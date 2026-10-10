// Package scorer ranks configured proxies for one credential from recent and
// long-term success statistics, and selects the next proxy to apply.
package scorer

import "sort"

// DefaultProxy identifies the pseudo proxy that collects observations made
// while a credential has no plugin proxy applied yet.
const DefaultProxy = "default"

// RecentWeight is the weight of the recent-window success rate in the score.
const RecentWeight = 0.7

// LongTermWeight is the weight of the long-term success rate in the score.
const LongTermWeight = 0.3

// Stats holds observation counters for one proxy and credential.
type Stats struct {
	// RecentSuccess counts successful observations inside the recent window.
	RecentSuccess int64
	// RecentTotal counts all observations inside the recent window.
	RecentTotal int64
	// LongSuccess counts successful observations over all history.
	LongSuccess int64
	// LongTotal counts all observations over all history.
	LongTotal int64
}

// Score blends the recent-window success rate with the long-term success
// rate, both smoothed with Laplace (add-one) priors so unseen or barely
// tested proxies stay near 0.5 instead of collapsing to 0 or 1.
func Score(stats Stats) float64 {
	recentRate := laplace(stats.RecentSuccess, stats.RecentTotal)
	longRate := laplace(stats.LongSuccess, stats.LongTotal)
	return RecentWeight*recentRate + LongTermWeight*longRate
}

func laplace(success, total int64) float64 {
	return (float64(success) + 1) / (float64(total) + 2)
}

// Select picks the next proxy for one credential.
//
// candidates lists the configured proxy URLs in configuration order and also
// bounds the result: proxies outside candidates are never selected.
//
// untried reports whether each candidate still has no observation at all.
// Untried candidates win over scored ones so every proxy eventually gets
// exercised. Ties in score keep the current proxy when it is among them and
// otherwise fall back to configuration order.
//
// current is the proxy currently applied for the credential; it may be empty
// or DefaultProxy when the credential still runs on the global default.
// Select never returns current when that would keep an unset or pseudo proxy;
// it always returns one of candidates.
func Select(candidates []string, untried map[string]bool, current string, stats map[string]Stats) string {
	if len(candidates) == 0 {
		return ""
	}
	for _, proxy := range candidates {
		if untried[proxy] {
			return proxy
		}
	}
	best := ""
	bestScore := 0.0
	for _, proxy := range candidates {
		score := Score(stats[proxy])
		if best == "" || score > bestScore {
			best = proxy
			bestScore = score
			continue
		}
		if score == bestScore && proxy == current {
			best = proxy
		}
	}
	return best
}

// Ranked orders candidates by descending score. Ties keep configuration
// order. It is exposed for diagnostics and tests.
func Ranked(candidates []string, stats map[string]Stats) []string {
	ranked := append([]string(nil), candidates...)
	sort.SliceStable(ranked, func(i, j int) bool {
		return Score(stats[ranked[i]]) > Score(stats[ranked[j]])
	})
	return ranked
}
