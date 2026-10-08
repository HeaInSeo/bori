package controllers

import (
	"context"
	"sync"
	"testing"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/HeaInSeo/bori/pkg/investigate"
	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// cuttingLookup wraps every provider so that the first call for subject
// blocks until its context ends (the run slice), then delegates.
type cuttingLookup struct {
	inner   investigate.ProviderLookup
	subject string
	mu      sync.Mutex
	cut     bool
}

func (c *cuttingLookup) Lookup(id operations.ProviderIdentity) (providers.Provider, bool) {
	p, ok := c.inner.Lookup(id)
	if !ok {
		return p, ok
	}
	return cutter{c, p}, true
}

type cutter struct {
	c     *cuttingLookup
	inner providers.Provider
}

func (s cutter) Observe(ctx context.Context, req providers.Request) providers.Result {
	s.c.mu.Lock()
	block := !s.c.cut && req.Subject.ResolvedUID == s.c.subject
	if block {
		s.c.cut = true
	}
	s.c.mu.Unlock()
	if block {
		<-ctx.Done()
		return providers.Result{Unavailable: "transport-error"}
	}
	return s.inner.Observe(ctx, req)
}

// Codex r4207335833 regression: the last target (y) of a shared run slice is
// cut mid-call and its episode stays Active. The reconcile must requeue at
// the 1s floor — not the 30s poll interval, beyond the 10s episode
// deadline — and the next reconcile resumes that same episode.
func TestO3ActiveEpisodeRequeuesBeforeDeadline(t *testing.T) {
	e := newO3Env(t)
	l := investigate.ReferenceLimits()
	l.RunSlice = 100 * time.Millisecond
	e.r.Investigator = investigate.New(l, e.clk.now)
	e.r.Providers = &cuttingLookup{inner: e.lookup, subject: "w-y"}

	res, err := e.r.Reconcile(context.Background(), ctrl.Request{})
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := e.r.Investigator.Episode("t-y")
	if cut.State != investigate.Active || cut.Calls == 0 {
		t.Fatalf("precondition: y cut while active, got %+v", cut)
	}
	if res.RequeueAfter != minRequeue {
		t.Fatalf("requeue %s with an active episode, want the %s floor (deadline in %s, interval %s)",
			res.RequeueAfter, minRequeue, cut.Deadline.Sub(e.clk.now()), e.r.RequeueInterval)
	}
	if !e.clk.now().Add(res.RequeueAfter).Before(cut.Deadline) {
		t.Fatal("requeue lands after the episode deadline")
	}
	// y alone holds no evidence, so without the active-episode wake the
	// requeue would be the 30s default interval.
	onlyY := investigate.Queries{"t-y": {"api-serving": investigate.Query{}}}
	if d := e.r.requeueAfter(onlyY, e.clk.now()); d != minRequeue {
		t.Fatalf("requeue %s for y's active episode, want %s (default %s)", d, minRequeue, e.r.RequeueInterval)
	}

	e.clk.add(res.RequeueAfter)
	res, err = e.r.Reconcile(context.Background(), ctrl.Request{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.r.Investigator.Episode("t-y")
	if !got.Started.Equal(cut.Started) || !got.Deadline.Equal(cut.Deadline) || got.Calls <= cut.Calls {
		t.Fatalf("episode reset: %+v → %+v", cut, got)
	}
	if got.State == investigate.Active || got.Calls > l.MaxCallsPerEpisode {
		t.Fatalf("episode not finished within its allowance: %+v", got)
	}
	if st := e.r.Investigator.Stats(); st.EpisodesStarted != 3 {
		t.Fatalf("%d episodes started, want one per target", st.EpisodesStarted)
	}
	// With no active work left, the requeue no longer sits at the floor.
	if res.RequeueAfter <= minRequeue {
		t.Fatalf("requeue %s collapsed to the floor after the episode finished", res.RequeueAfter)
	}
	t.Logf("y episode %s calls %d→%d; requeue %s", got.State, cut.Calls, got.Calls, res.RequeueAfter)
}
