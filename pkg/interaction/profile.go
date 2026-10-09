package interaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"unicode/utf8"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
)

// Profile is the operator's declared reference profile: response candidates
// and their human-decision boundary. It is explicit input, never inferred
// from provider text or a model. It grants no execution authority: the
// projection only displays what is declared and why it can or cannot be
// taken now. A changed Revision is a new identity for every summary.
type Profile struct {
	Revision  string     `json:"revision"`
	Responses []Response `json:"responses"`
}

// Response declares one response candidate for one capability of one exact
// target.
type Response struct {
	Action ActionRef `json:"action"`
	Target TargetRef `json:"target"`
	// For is the capability the response addresses.
	For opsv1.CapabilityType `json:"for"`
	// States the response is declared for; default DEGRADED and UNAVAILABLE.
	States []string `json:"states,omitempty"`
	// Owner is the execution owner; empty is reported as undeclared.
	Owner string `json:"owner,omitempty"`
	// RequiresApproval declares that the owner's policy needs explicit
	// human approval.
	RequiresApproval bool `json:"requiresApproval,omitempty"`
	// Risks declares possible data loss, service interruption or security
	// scope expansion; any risk needs a human decision.
	Risks []string `json:"risks,omitempty"`
	// Preconditions are capabilities of the same target that must be
	// AVAILABLE on current evidence for the response to be safe.
	Preconditions []opsv1.CapabilityType `json:"preconditions,omitempty"`
}

// ActionRef is a declared action identity.
type ActionRef struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

func (a ActionRef) String() string { return a.Name + "@" + a.Revision }

// TargetRef names the exact OperationalTarget a response is declared for.
// A set UID pins the instance: a recreated target does not match.
type TargetRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

// Declared risk kinds.
const (
	RiskDataLoss            = "data-loss"
	RiskServiceInterruption = "service-interruption"
	RiskSecurityScope       = "security-scope-expansion"
)

// Profile bounds.
const (
	MaxResponses     = 128
	maxPreconditions = 8
	maxRisks         = 4
	maxOwner         = 128
)

var (
	nameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	risks  = map[string]bool{RiskDataLoss: true, RiskServiceInterruption: true, RiskSecurityScope: true}
	states = map[string]bool{"DEGRADED": true, "UNAVAILABLE": true, "UNKNOWN": true}
)

// LoadProfile reads and validates a profile file.
func LoadProfile(path string) (*Profile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseProfile(b)
}

// ParseProfile decodes a profile strictly (unknown fields are errors) and
// validates its bounds.
func ParseProfile(b []byte) (*Profile, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var p Profile
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("interaction profile: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks the profile's bounds and vocabularies.
func (p *Profile) Validate() error {
	if p.Revision == "" || len(p.Revision) > 63 {
		return errors.New("interaction profile: revision must be 1-63 characters")
	}
	if len(p.Responses) > MaxResponses {
		return fmt.Errorf("interaction profile: %d responses, at most %d", len(p.Responses), MaxResponses)
	}
	for i, r := range p.Responses {
		if err := r.validate(); err != nil {
			return fmt.Errorf("interaction profile: response %d: %w", i, err)
		}
	}
	return nil
}

func (r Response) validate() error {
	switch {
	case !nameRE.MatchString(r.Action.Name) || len(r.Action.Name) > 63:
		return fmt.Errorf("action name %q invalid", r.Action.Name)
	case r.Action.Revision == "" || len(r.Action.Revision) > 63:
		return errors.New("action revision must be 1-63 characters")
	case !nameRE.MatchString(r.Target.Namespace) || !nameRE.MatchString(r.Target.Name):
		return errors.New("target needs a namespace and a name")
	case len(r.Target.UID) > 128:
		return errors.New("target uid too long")
	case checkType(r.For) != nil:
		return fmt.Errorf("for: %w", checkType(r.For))
	case len(r.Owner) > maxOwner:
		return errors.New("owner too long")
	case len(r.Risks) > maxRisks:
		return errors.New("too many risks")
	case len(r.Preconditions) > maxPreconditions:
		return errors.New("too many preconditions")
	}
	for _, s := range r.States {
		if !states[s] {
			return fmt.Errorf("state %q is not DEGRADED, UNAVAILABLE or UNKNOWN", s)
		}
	}
	for _, k := range r.Risks {
		if !risks[k] {
			return fmt.Errorf("risk %q unknown", k)
		}
	}
	for i, c := range r.Preconditions {
		if err := checkType(c); err != nil {
			return fmt.Errorf("precondition %d: %w", i, err)
		}
	}
	return nil
}

// checkType holds each field of a declared capability type to the
// CapabilityType API bounds (domain 1–253, name 1–63, revision 1–63
// characters, counted as the API server counts them). A type outside them
// can never name a contract capability, and its key would not fit a status
// subject, so the profile is rejected at load instead.
func checkType(c opsv1.CapabilityType) error {
	for _, f := range []struct {
		field, v string
		max      int
	}{{"domain", c.Domain, 253}, {"name", c.Name, 63}, {"revision", c.Revision, 63}} {
		if n := utf8.RuneCountInString(f.v); n < 1 || n > f.max {
			return fmt.Errorf("capability %s must be 1-%d characters, got %d", f.field, f.max, n)
		}
	}
	return nil
}

func (r Response) triggers(state string) bool {
	if len(r.States) == 0 {
		return state == "DEGRADED" || state == "UNAVAILABLE"
	}
	for _, s := range r.States {
		if s == state {
			return true
		}
	}
	return false
}
