// Package actiontest provides the fake/reference actor, approval verifier
// and fault-injecting journal used to verify the O5 flow (L4). They model
// behaviour only: a simulated journal or signature is not evidence of real
// durability or operational authority.
package actiontest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/HeaInSeo/bori/pkg/action"
)

// ErrStaleFence is returned when a handoff carries a fence lower than one
// the actor has already seen from this BORI journal.
var ErrStaleFence = errors.New("actor: stale fence")

// Behaviour scripts what the actor does with one handoff.
type Behaviour int

const (
	// Succeed: accept, then report Succeeded on the next poll.
	Succeed Behaviour = iota
	// Fail: accept, then report Failed.
	Fail
	// AcceptOnly: accept and stay Pending forever.
	AcceptOnly
	// Refuse: answer Accepted=false; nothing is executed.
	Refuse
	// Silent: record nothing and return a transport error.
	Silent
)

// Actor is the reference ActionProvider. It deduplicates by key, rejects
// stale fences, counts real executions per key and logs every handoff.
type Actor struct {
	mu        sync.Mutex
	Default   Behaviour
	PerAction map[string]Behaviour // by action name@revision
	// LoseReceipts makes Submit execute but return a transport error, as
	// if the answer was lost after acceptance.
	LoseReceipts int
	// DropSubmits makes the next Submits never reach the actor, as if BORI
	// crashed after persisting and before sending.
	DropSubmits int
	// OnExecute is called once per newly accepted key (the actuation the
	// real authority would perform); tests use it to change the world.
	OnExecute func(h action.Handoff)

	highFence  uint64
	jobs       map[string]*job
	Handoffs   []action.Handoff // every Submit call, in order
	Rejected   int              // stale-fence submits
	Executions map[string]int   // newly accepted executions per key
	// Forge overrides the next Result answers (wrong key/fence/digest).
	Forge []action.Result
}

type job struct {
	h     action.Handoff
	b     Behaviour
	polls int
}

// NewActor returns an actor whose handoffs behave as b.
func NewActor(b Behaviour) *Actor {
	return &Actor{Default: b, PerAction: map[string]Behaviour{}, jobs: map[string]*job{}, Executions: map[string]int{}}
}

// Submit implements action.Provider.
func (a *Actor) Submit(_ context.Context, h action.Handoff) (action.Receipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.DropSubmits > 0 {
		a.DropSubmits--
		return action.Receipt{}, errors.New("actor: not delivered")
	}
	a.Handoffs = append(a.Handoffs, h)
	if h.Fence < a.highFence {
		a.Rejected++
		return action.Receipt{}, ErrStaleFence
	}
	a.highFence = h.Fence
	b, ok := a.PerAction[h.Action.String()]
	if !ok {
		b = a.Default
	}
	if b == Silent {
		return action.Receipt{}, errors.New("actor: unreachable")
	}
	j, seen := a.jobs[h.Key]
	if !seen {
		j = &job{h: h, b: b}
		a.jobs[h.Key] = j
		if b != Refuse {
			a.Executions[h.Key]++
			if a.OnExecute != nil {
				a.OnExecute(h)
			}
		}
	}
	j.h.Fence = h.Fence // a newer owner of the same key
	rc := action.Receipt{Key: h.Key, Fence: h.Fence, Digest: h.Digest, Accepted: j.b != Refuse, Ref: "actor:" + h.Key}
	if a.LoseReceipts > 0 {
		a.LoseReceipts--
		return action.Receipt{}, errors.New("actor: receipt lost")
	}
	return rc, nil
}

// Result implements action.Provider.
func (a *Actor) Result(_ context.Context, key string) (action.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.Forge) > 0 {
		f := a.Forge[0]
		a.Forge = a.Forge[1:]
		return f, nil
	}
	j, ok := a.jobs[key]
	if !ok {
		return action.Result{Key: key, State: action.ResultUnknown}, nil
	}
	j.polls++
	r := action.Result{Key: key, Fence: j.h.Fence, Digest: j.h.Digest, Ref: "actor:" + key}
	switch j.b {
	case Succeed:
		r.State = action.ResultSucceeded
	case Fail, Refuse:
		r.State = action.ResultFailed
	default:
		r.State = action.ResultPending
	}
	return r, nil
}

// Settle changes how an accepted handoff ends (the actuation finishing
// later).
func (a *Actor) Settle(key string, b Behaviour) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if j, ok := a.jobs[key]; ok {
		j.b = b
	}
}

// Live reports whether the actor still holds live responsibility for an
// executed key: accepted and not yet ended. It is the actor's own fact,
// independent of what BORI recorded, and is neither a poll nor forged.
func (a *Actor) Live(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	j, ok := a.jobs[key]
	return ok && j.b == AcceptOnly
}

// Ended reports whether an executed key has ended (succeeded or failed) at
// the actor.
func (a *Actor) Ended(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	j, ok := a.jobs[key]
	return ok && (j.b == Succeed || j.b == Fail)
}

// TotalExecutions sums real executions over all keys.
func (a *Actor) TotalExecutions() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.Executions {
		n += c
	}
	return n
}

// ExecutedKeys returns the keys executed at least once.
func (a *Actor) ExecutedKeys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for k := range a.Executions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Signer is a reference HMAC approval identity: each principal has a key
// only it holds. It implements action.Verifier.
type Signer struct{ keys map[string][]byte }

// NewSigner registers principals with their keys.
func NewSigner(keys map[string]string) *Signer {
	s := &Signer{keys: map[string][]byte{}}
	for p, k := range keys {
		s.keys[p] = []byte(k)
	}
	return s
}

func payload(d action.Decision) []byte {
	return []byte(fmt.Sprintf("%s|%s|%s|%s|%s|%d", d.ID, d.ProposalID, d.Digest, d.Principal, d.Verdict, d.DecidedAt.UnixNano()))
}

// Sign returns d signed with the given key (the caller proves possession).
func Sign(d action.Decision, key string) action.Decision {
	m := hmac.New(sha256.New, []byte(key))
	m.Write(payload(d))
	d.Proof = m.Sum(nil)
	return d
}

// Verify implements action.Verifier.
func (s *Signer) Verify(d action.Decision) bool {
	k, ok := s.keys[d.Principal]
	if !ok || len(d.Proof) == 0 {
		return false
	}
	m := hmac.New(sha256.New, k)
	m.Write(payload(d))
	return hmac.Equal(m.Sum(nil), d.Proof)
}

// Decisions is a static decision source.
type Decisions struct {
	mu sync.Mutex
	ds []action.Decision
}

// Add submits a decision.
func (s *Decisions) Add(d action.Decision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ds = append(s.ds, d)
}

// Decisions implements action.DecisionSource.
func (s *Decisions) Decisions(context.Context) ([]action.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]action.Decision(nil), s.ds...), nil
}

// FaultJournal wraps a journal and fails selected writes.
type FaultJournal struct {
	action.Journal
	mu sync.Mutex
	// FailPuts fails the next n writes.
	FailPuts int
	// FailPhase fails every write that sets this phase.
	FailPhase action.Phase
	Failed    int
}

// Put implements action.Journal.
func (f *FaultJournal) Put(ctx context.Context, r action.Record) (action.Record, error) {
	f.mu.Lock()
	fail := f.FailPuts > 0 || (f.FailPhase != "" && r.Phase == f.FailPhase)
	if f.FailPuts > 0 {
		f.FailPuts--
	}
	if fail {
		f.Failed++
	}
	f.mu.Unlock()
	if fail {
		return action.Record{}, errors.New("journal: injected write failure")
	}
	return f.Journal.Put(ctx, r)
}

// Clock is a manual clock.
type Clock struct{ T time.Time }

// Add advances the clock.
func (c *Clock) Add(d time.Duration) { c.T = c.T.Add(d) }
