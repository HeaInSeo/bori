package investigate

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// cutOnce blocks the first call for subject (any subject when empty) until
// its context ends — a call cut by the run slice — and delegates afterwards.
type cutOnce struct {
	mu      sync.Mutex
	subject string
	cut     bool
	inner   providers.Provider
}

func (c *cutOnce) Observe(ctx context.Context, req providers.Request) providers.Result {
	c.mu.Lock()
	block := !c.cut && (c.subject == "" || req.Subject.ResolvedUID == c.subject)
	if block {
		c.cut = true
	}
	c.mu.Unlock()
	if block {
		<-ctx.Done()
		return providers.Result{Unavailable: "transport-error"}
	}
	return c.inner.Observe(ctx, req)
}

// minRequeue mirrors the controller's requeue floor.
const minRequeue = time.Second

// sliceCutWorld leaves t-svc's episode Active after its second call
// (available-replicas) is cut by a short real-time run slice; the evaluation
// clock stays at t0.
func sliceCutWorld(t *testing.T) *world {
	t.Helper()
	l := ReferenceLimits()
	l.RunSlice = 30 * time.Millisecond
	w := newWorld(l)
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.reg.ps[kubeID] = &cutOnce{inner: w.kube}
	w.run(context.Background())
	e := episode(t, w)
	if e.State != Active || e.Calls != 2 {
		t.Fatalf("precondition: slice-cut active episode with 2 calls, got %+v", e)
	}
	return w
}

// Codex r4207335833 regression: a still-valid, queryable episode left Active
// by the run slice asks for a wake before its original deadline, and the
// next Run resumes it with its consumed allowance — no reset, no new episode.
func TestSliceCutEpisodeResumesBeforeDeadline(t *testing.T) {
	w := sliceCutWorld(t)
	before := episode(t, w)
	now := w.clk.now()
	wake := w.iv.NextWake(w.queries(), now)
	if wake.IsZero() || !wake.Add(minRequeue).Before(before.Deadline) {
		t.Fatalf("wake %v (+%s floor) is not before the deadline %v", wake, minRequeue, before.Deadline)
	}

	w.clk.advance(minRequeue)
	w.run(context.Background())
	after := episode(t, w)
	if !after.Started.Equal(before.Started) || !after.Deadline.Equal(before.Deadline) {
		t.Fatalf("episode restarted: %+v → %+v", before, after)
	}
	if after.Calls != before.Calls+1 || after.Steps != before.Steps+1 {
		t.Fatalf("calls/steps %d/%d → %d/%d, want exactly one more", before.Calls, before.Steps, after.Calls, after.Steps)
	}
	// The cut available-replicas call never reached the inner provider;
	// only the remaining slot is dispatched on resume.
	if got := w.kube.callList(); len(got) != 1 || got[0] != "ready-replicas" {
		t.Fatalf("kube calls %v: the cut slot was re-dispatched or the remaining one was not", got)
	}
	if st := w.iv.Stats(); st.EpisodesStarted != 1 {
		t.Fatalf("%d episodes started", st.EpisodesStarted)
	}
	if after.active() {
		t.Fatalf("episode still active: %+v", after)
	}
	if capState(w.assess(), "export") != operations.Available {
		t.Fatal("resumed call did not produce evidence")
	}
	// Finished work does not pin the requeue to its floor.
	if wake := w.iv.NextWake(w.queries(), w.clk.now()); !wake.IsZero() && !wake.After(w.clk.now().Add(minRequeue)) {
		t.Fatalf("wake %v after the episode finished", wake)
	}
}

// Codex r4207335833 regression: a target late in a shared slice whose call
// is cut resumes first on the next Run, inside its own original deadline.
func TestLateTargetInSharedSliceResumesBeforeDeadline(t *testing.T) {
	l := ReferenceLimits()
	l.RunSlice = 30 * time.Millisecond
	w := newWorld(l)
	w.base.Targets = append(w.base.Targets, svcTarget("t-svc2", "w-svc2"), svcTarget("t-svc3", "w-svc3"))
	w.http.answers["api-serving"] = boolAt(w.clk, true)
	w.kube.answers["available-replicas"] = intAt(w.clk, 3)
	w.kube.answers["ready-replicas"] = intAt(w.clk, 3)
	w.reg.ps[kubeID] = &cutOnce{subject: "w-svc3", inner: w.kube}
	w.run(context.Background())
	late, _ := w.iv.Episode("t-svc3")
	if late.State != Active || late.Calls == 0 {
		t.Fatalf("precondition: late target cut while active, got %+v", late)
	}
	for _, uid := range []string{"t-svc", "t-svc2"} {
		if e, _ := w.iv.Episode(uid); e.active() {
			t.Fatalf("%s still active", uid)
		}
	}
	now := w.clk.now()
	if wake := w.iv.NextWake(w.queries(), now); wake.IsZero() || !wake.Add(minRequeue).Before(late.Deadline) {
		t.Fatalf("wake %v is not before the late target's deadline %v", wake, late.Deadline)
	}
	w.clk.advance(minRequeue)
	w.run(context.Background())
	e, _ := w.iv.Episode("t-svc3")
	if !e.Started.Equal(late.Started) || e.Calls <= late.Calls || e.active() {
		t.Fatalf("late target did not resume its episode: %+v → %+v", late, e)
	}
	if e.Calls > l.MaxCallsPerEpisode {
		t.Fatalf("%d calls", e.Calls)
	}
}

// A remaining deadline below the requeue floor still wakes once; that Run
// ends the episode as DeadlineHit without a call. An expired active episode
// never asks for an immediate wake, so nothing polls at the floor.
func TestNearAndExpiredDeadlineDoNotPoll(t *testing.T) {
	w := sliceCutWorld(t)
	e := episode(t, w)
	w.clk.advance(e.Deadline.Sub(w.clk.now()) - 500*time.Millisecond)
	if wake := w.iv.NextWake(w.queries(), w.clk.now()); !wake.Equal(w.clk.now()) {
		t.Fatalf("near-deadline wake %v", wake)
	}
	w.clk.advance(minRequeue) // the floor lands past the deadline
	now := w.clk.now()
	if wake := w.iv.NextWake(w.queries(), now); !wake.IsZero() && !wake.After(now) {
		t.Fatalf("expired active episode wakes at %v", wake)
	}
	calls := len(w.kube.callList()) + len(w.http.callList())
	w.run(context.Background())
	if got := episode(t, w); got.State != DeadlineHit || got.Calls != e.Calls {
		t.Fatalf("episode %+v, want DeadlineHit with %d calls", got, e.Calls)
	}
	if n := len(w.kube.callList()) + len(w.http.callList()); n != calls {
		t.Fatalf("%d calls after the deadline", n-calls)
	}
	if wake := w.iv.NextWake(w.queries(), now); !wake.IsZero() && !wake.After(now.Add(minRequeue)) {
		t.Fatalf("wake %v pins the requeue to its floor", wake)
	}
}

// Removed, invalid and query-less targets and shutdown never keep an
// immediate wake for an episode that can no longer progress.
func TestUnfinishableActiveWorkDoesNotWake(t *testing.T) {
	noFloor := func(t *testing.T, w *world, qs Queries) {
		t.Helper()
		now := w.clk.now()
		if wake := w.iv.NextWake(qs, now); !wake.IsZero() && !wake.After(now.Add(minRequeue)) {
			t.Fatalf("wake %v", wake)
		}
	}
	t.Run("removed", func(t *testing.T) {
		w := sliceCutWorld(t)
		noFloor(t, w, Queries{})
		w.iv.Run(context.Background(), w.base, Queries{})
		if e := episode(t, w); e.State != Superseded {
			t.Fatalf("%+v", e)
		}
		noFloor(t, w, w.queries())
	})
	t.Run("invalid", func(t *testing.T) {
		w := sliceCutWorld(t)
		makeInvalid(&w.base.Targets[0])
		w.run(context.Background())
		if e := episode(t, w); e.State != TargetInvalid {
			t.Fatalf("%+v", e)
		}
		noFloor(t, w, w.queries())
	})
	t.Run("no-query", func(t *testing.T) {
		w := sliceCutWorld(t)
		qs := Queries{"t-svc": {}}
		noFloor(t, w, qs)
		calls := len(w.kube.callList()) + len(w.http.callList())
		w.iv.Run(context.Background(), w.base, qs)
		if e := episode(t, w); e.active() {
			t.Fatalf("%+v", e)
		}
		if n := len(w.kube.callList()) + len(w.http.callList()); n != calls {
			t.Fatal("dispatch without a query")
		}
		noFloor(t, w, qs)
	})
	t.Run("shutdown", func(t *testing.T) {
		l := ReferenceLimits()
		l.RunSlice = 5 * time.Second
		w := newWorld(l)
		w.http.answers["api-serving"] = boolAt(w.clk, true)
		w.reg.ps[kubeID] = &cutOnce{inner: w.kube}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(20 * time.Millisecond); cancel() }()
		w.run(ctx)
		if e := episode(t, w); e.State != Cancelled {
			t.Fatalf("%+v", e)
		}
		noFloor(t, w, w.queries())
	})
}
