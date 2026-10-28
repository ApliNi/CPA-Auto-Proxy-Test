// Package scorer ranks configured proxies for one credential from recent and
// long-term success statistics, empirical priors, and health state, and selects
// the next proxy to apply.
package scorer

import (
	"sort"
	"time"
)

// DefaultProxy identifies the pseudo proxy that collects observations made
// while a credential has no plugin proxy applied yet.
const DefaultProxy = "default"

const (
	// RecentWeight is the maximum weight of the recent-window success rate in the score.
	RecentWeight = 0.7
	// LongTermWeight is the minimum weight of the long-term success rate in the score.
	LongTermWeight = 0.3
	// DefaultPrior is the uninformative Laplace prior mean.
	DefaultPrior = 0.5
	// DefaultPriorWeight is the equivalent sample count of the prior.
	DefaultPriorWeight = 2.0
	// DefaultMinRecentSamples is the number of recent observations required
	// for full confidence in recent-window statistics.
	DefaultMinRecentSamples = 5
)

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

// Score blends the recent-window success rate with the long-term success rate
// using the default prior and confidence threshold. When recent observations
// are absent, it cleanly defaults to long-term performance instead of artificially
// boosting idle failed proxies.
func Score(stats Stats) float64 {
	return ScoreWithPrior(stats, DefaultPrior, DefaultMinRecentSamples)
}

// ScoreWithPrior blends recent and long-term success rates with an empirical prior
// and sample-count confidence scaling.
func ScoreWithPrior(stats Stats, prior float64, minRecentSamples int) float64 {
	if prior <= 0 || prior >= 1 {
		prior = DefaultPrior
	}
	if minRecentSamples <= 0 {
		minRecentSamples = DefaultMinRecentSamples
	}
	longRate := smoothRate(stats.LongSuccess, stats.LongTotal, prior)
	if stats.RecentTotal <= 0 {
		return longRate
	}
	recentRate := smoothRate(stats.RecentSuccess, stats.RecentTotal, prior)
	conf := float64(stats.RecentTotal) / float64(minRecentSamples)
	if conf > 1.0 {
		conf = 1.0
	}
	effRecentWeight := RecentWeight * conf
	effLongWeight := 1.0 - effRecentWeight
	return effRecentWeight*recentRate + effLongWeight*longRate
}

func smoothRate(success, total int64, prior float64) float64 {
	return (float64(success) + prior*DefaultPriorWeight) / (float64(total) + DefaultPriorWeight)
}

// SelectOptions parametrizes candidate selection.
type SelectOptions struct {
	// Candidates lists the configured proxy URLs in configuration order.
	Candidates []string
	// Untried reports whether each candidate has no observation for this credential.
	Untried map[string]bool
	// Current is the proxy currently applied to the credential.
	Current string
	// Stats holds per-proxy observation counters for this credential.
	Stats map[string]Stats
	// Exclude specifies a proxy that should not be selected (e.g. the currently
	// failing proxy when switching due to a failure).
	Exclude string
	// Cooldowns maps proxy URLs to the expiration time of their failure cooldowns.
	Cooldowns map[string]time.Time
	// GlobalPriors maps proxy URLs to fleet-wide empirical success rates.
	GlobalPriors map[string]float64
	// MinRecentSamples overrides the default confidence sample threshold.
	MinRecentSamples int
	// Now is the reference time used to evaluate cooldown expirations.
	Now time.Time
}

// Select picks the next proxy using default options for backwards compatibility.
func Select(candidates []string, untried map[string]bool, current string, stats map[string]Stats) string {
	return SelectCandidate(SelectOptions{
		Candidates: candidates,
		Untried:    untried,
		Current:    current,
		Stats:      stats,
	})
}

// SelectCandidate picks the optimal proxy candidate given recent stats, failure
// context, empirical priors, and cooldowns.
func SelectCandidate(opts SelectOptions) string {
	if len(opts.Candidates) == 0 {
		return ""
	}

	// 1. Determine candidate pool. When switching on failure, exclude the failing proxy
	// if multiple candidates are configured.
	pool := make([]string, 0, len(opts.Candidates))
	for _, proxy := range opts.Candidates {
		if opts.Exclude != "" && len(opts.Candidates) > 1 && proxy == opts.Exclude {
			continue
		}
		pool = append(pool, proxy)
	}
	if len(pool) == 0 {
		pool = append(pool, opts.Candidates...)
	}

	// 2. Classify candidates into active vs cooling down.
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	var activePool, coolingPool []string
	for _, proxy := range pool {
		if cd, ok := opts.Cooldowns[proxy]; ok && now.Before(cd) {
			coolingPool = append(coolingPool, proxy)
		} else {
			activePool = append(activePool, proxy)
		}
	}

	// Prefer candidates that are not in failure cooldown.
	eligible := activePool
	allCooling := false
	if len(eligible) == 0 {
		eligible = coolingPool
		allCooling = true
	}

	// 3. Untried candidates priority: rank untried candidates by global empirical prior,
	// allowing fleet-wide reliable nodes to be tested before historically unreliable nodes.
	var untriedEligible []string
	for _, proxy := range eligible {
		if opts.Untried != nil && opts.Untried[proxy] {
			untriedEligible = append(untriedEligible, proxy)
		}
	}
	if len(untriedEligible) > 0 {
		bestUntried := untriedEligible[0]
		bestPrior := DefaultPrior
		if p, ok := opts.GlobalPriors[bestUntried]; ok && p > 0 {
			bestPrior = p
		}
		for _, proxy := range untriedEligible[1:] {
			prior := DefaultPrior
			if p, ok := opts.GlobalPriors[proxy]; ok && p > 0 {
				prior = p
			}
			if prior > bestPrior {
				bestUntried = proxy
				bestPrior = prior
			}
		}
		return bestUntried
	}

	// 4. Scored candidates evaluation.
	minSamples := opts.MinRecentSamples
	if minSamples <= 0 {
		minRecentSamples := DefaultMinRecentSamples
		minSamples = minRecentSamples
	}

	best := ""
	bestScore := -1.0
	var bestExpiry time.Time

	for _, proxy := range eligible {
		prior := DefaultPrior
		if p, ok := opts.GlobalPriors[proxy]; ok && p > 0 {
			prior = p
		}
		score := ScoreWithPrior(opts.Stats[proxy], prior, minSamples)

		if allCooling {
			expiry := opts.Cooldowns[proxy]
			if best == "" || expiry.Before(bestExpiry) {
				best = proxy
				bestScore = score
				bestExpiry = expiry
				continue
			}
			if expiry.Equal(bestExpiry) {
				if score > bestScore {
					best = proxy
					bestScore = score
					continue
				}
				if score == bestScore && proxy == opts.Current && (opts.Exclude == "" || opts.Exclude != opts.Current) {
					best = proxy
				}
			}
			continue
		}

		if best == "" || score > bestScore {
			best = proxy
			bestScore = score
			continue
		}
		if score == bestScore && proxy == opts.Current && (opts.Exclude == "" || opts.Exclude != opts.Current) {
			best = proxy
		}
	}

	return best
}

// Ranked orders candidates by descending score with default priors.
// Ties keep configuration order. It is exposed for diagnostics and tests.
func Ranked(candidates []string, stats map[string]Stats) []string {
	return RankedWithPriors(candidates, stats, nil, DefaultMinRecentSamples)
}

// RankedWithPriors orders candidates by descending composite score.
func RankedWithPriors(candidates []string, stats map[string]Stats, globalPriors map[string]float64, minSamples int) []string {
	ranked := append([]string(nil), candidates...)
	sort.SliceStable(ranked, func(i, j int) bool {
		priorI := DefaultPrior
		if p, ok := globalPriors[ranked[i]]; ok && p > 0 {
			priorI = p
		}
		priorJ := DefaultPrior
		if p, ok := globalPriors[ranked[j]]; ok && p > 0 {
			priorJ = p
		}
		return ScoreWithPrior(stats[ranked[i]], priorI, minSamples) >
			ScoreWithPrior(stats[ranked[j]], priorJ, minSamples)
	})
	return ranked
}
