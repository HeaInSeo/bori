// Package investigate is the O3 bounded, evidence-conditioned investigation
// loop. It decides which single authorized evidence query to run next from
// the current O1 assessment, keeps falsifiable candidates, and stops when the
// candidates are decided, nothing useful is queryable, or the episode budget
// is spent. It holds the process-local evidence cache.
//
// It never decides capability truth: every assessment comes from
// pkg/operations.Evaluate over the held observations. Candidates are
// hypotheses about which referenced requirement explains a non-AVAILABLE
// capability; they are never promoted to facts or RCA.
package investigate

import (
	"time"

	"github.com/HeaInSeo/bori/pkg/providers"
)

// Limits is the reference profile budget. The values are a reference
// configuration for this bounded experiment, not a public semantic standard.
type Limits struct {
	// MaxCallsPerEpisode bounds real provider calls (each call is exactly one
	// Kubernetes GET or one HTTP GET; adapters do no retries, redirects or
	// fan-out).
	MaxCallsPerEpisode int
	// MaxStepsPerEpisode bounds planner iterations.
	MaxStepsPerEpisode int
	// EpisodeDeadline bounds an episode's elapsed time from its start.
	EpisodeDeadline time.Duration
	// PerCallTimeout bounds one provider call (also capped by the deadline).
	PerCallTimeout time.Duration
	// MaxCandidates bounds hypotheses kept per episode.
	MaxCandidates int
	// MaxTraceEntries bounds the per-episode trace.
	MaxTraceEntries int
	// MaxResidentEpisodes bounds episode records kept in memory.
	MaxResidentEpisodes int
	// MaxHeldObservations bounds cached applicability keys.
	MaxHeldObservations int
	// EpisodeCooldown is the minimum time between episode starts for one
	// OperationalTarget UID, whatever its identity or the outcome of the
	// previous episode. It caps both re-investigation and refresh cadence.
	EpisodeCooldown time.Duration
	// RefreshFraction of a slot's maxAge before expiry at which held
	// evidence becomes due for a freshness query.
	RefreshFraction float64
	// RunSlice bounds the wall time of one Run (one reconcile). Episodes not
	// finished in the slice stay active with their remaining budget.
	RunSlice time.Duration
}

// ReferenceLimits returns the reference profile values.
//
//   - 6 calls / 8 steps: a contract in the reference fixtures has at most a
//     handful of slots per target; every slot can be queried once per episode
//     with room for planner steps that dispatch nothing.
//   - 10s deadline, 2s per call: a few sequential calls within one reconcile.
//   - Calls are strictly sequential (concurrency 1) for determinism.
//   - 10s cooldown with refresh at half of maxAge: a 30s-maxAge slot is
//     refreshed 15s before expiry, so steady state never lapses into UNKNOWN,
//     while one target cannot spend more than one episode budget per 10s.
func ReferenceLimits() Limits {
	return Limits{
		MaxCallsPerEpisode:  6,
		MaxStepsPerEpisode:  8,
		EpisodeDeadline:     10 * time.Second,
		PerCallTimeout:      2 * time.Second,
		MaxCandidates:       32,
		MaxTraceEntries:     64,
		MaxResidentEpisodes: 256,
		MaxHeldObservations: 1024,
		EpisodeCooldown:     10 * time.Second,
		RefreshFraction:     0.5,
		RunSlice:            20 * time.Second,
	}
}

// ForConfig returns the reference limits with the per-call timeout taken
// from the provider configuration, so the investigator enforces the same
// bound the HTTP client is built with and Kubernetes-status calls get it too.
func ForConfig(cfg *providers.Config) (Limits, error) {
	l := ReferenceLimits()
	d, err := cfg.PerCallTimeout()
	if err != nil {
		return Limits{}, err
	}
	l.PerCallTimeout = d
	return l, nil
}
