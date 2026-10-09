package action

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// ErrConflict is returned by Journal.Put when the record's version is not
// the stored one: another writer (for example an engine with an older or
// newer epoch) changed it first.
var ErrConflict = errors.New("journal: version conflict")

// Journal is the durable execution log. Every handoff is written here
// before it is sent. Put is a compare-and-swap on Version: a new record
// has Version 0; the stored record is returned with the next version.
//
// The concrete store is OPEN (API v0.1 §19); see docs/operational-actions.md.
type Journal interface {
	// Durable reports whether a written record survives a process restart.
	// Handoff is refused on a non-durable journal.
	Durable() bool
	// Epoch claims journal ownership and returns a value greater than any
	// returned before; it is the fence of every send made by its holder.
	Epoch(ctx context.Context) (uint64, error)
	List(ctx context.Context) ([]Record, error)
	Put(ctx context.Context, r Record) (Record, error)
}

// MemJournal is an in-memory journal. It is not durable unless constructed
// as a simulation (tests model a surviving store by sharing one instance
// between engines); a simulation is never evidence of real durability.
type MemJournal struct {
	mu      sync.Mutex
	durable bool
	epoch   uint64
	recs    map[string]Record
}

// NewMemJournal returns the non-durable reference journal shipped with the
// operator: proposals are shown, nothing is handed off.
func NewMemJournal() *MemJournal { return &MemJournal{recs: map[string]Record{}} }

// NewSimulatedDurableJournal returns an in-memory journal that claims
// durability, for L4 simulations only.
func NewSimulatedDurableJournal() *MemJournal {
	return &MemJournal{durable: true, recs: map[string]Record{}}
}

// Durable implements Journal.
func (j *MemJournal) Durable() bool { return j.durable }

// Epoch implements Journal.
func (j *MemJournal) Epoch(context.Context) (uint64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.epoch++
	return j.epoch, nil
}

// List implements Journal; records are ordered by key.
func (j *MemJournal) List(context.Context) ([]Record, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Record, 0, len(j.recs))
	for _, r := range j.recs {
		out = append(out, clone(r))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Key < out[b].Key })
	return out, nil
}

// Put implements Journal.
func (j *MemJournal) Put(_ context.Context, r Record) (Record, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	cur, ok := j.recs[r.Key]
	switch {
	case !ok && r.Version != 0, ok && cur.Version != r.Version:
		return Record{}, ErrConflict
	}
	r.Version++
	j.recs[r.Key] = clone(r)
	return clone(r), nil
}

func clone(r Record) Record {
	r.Binding.Providers = append([]string(nil), r.Binding.Providers...)
	r.Binding.Dependencies = append([]string(nil), r.Binding.Dependencies...)
	r.Expected = append(r.Expected[:0:0], r.Expected...)
	r.RecoveryDetail = append(r.RecoveryDetail[:0:0], r.RecoveryDetail...)
	return r
}
