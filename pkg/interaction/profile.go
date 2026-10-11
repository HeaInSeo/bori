package interaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	// Execution makes the response eligible for an O5 proposal and handoff
	// to an external ActionProvider. Without it the response is display-only.
	Execution *Execution `json:"execution,omitempty"`
}

// Execution is the O5 action contract of a response: who carries it out,
// who may approve it, what it is expected to restore and what it may
// interrupt, and its bounded timeouts and retry budget. It grants BORI no
// mutation authority; it only says to whom and under which conditions a
// handoff may be made.
type Execution struct {
	// Provider names the registered ActionProvider the handoff goes to.
	Provider string `json:"provider"`
	// Approvers are the principals whose verified decision may approve or
	// reject the exact proposal. Required when a person must decide.
	Approvers []string `json:"approvers,omitempty"`
	// ExpectedImpact are the capabilities of this target the action is
	// expected to restore; it must include For. Recovery is judged on
	// exactly these.
	ExpectedImpact []opsv1.CapabilityType `json:"expectedImpact"`
	// MayInterrupt are capabilities of this target the action may
	// interrupt (blast radius). Any entry needs a human decision.
	MayInterrupt []opsv1.CapabilityType `json:"mayInterrupt,omitempty"`
	// AckTimeout bounds the wait for the provider's durable acceptance.
	AckTimeout metav1.Duration `json:"ackTimeout"`
	// CompletionTimeout bounds the wait for a result after acceptance.
	CompletionTimeout metav1.Duration `json:"completionTimeout"`
	// RecoveryWindow bounds the wait for post-execution evidence.
	RecoveryWindow metav1.Duration `json:"recoveryWindow"`
	// ApprovalTTL bounds the age of an approval decision (default 1h).
	ApprovalTTL metav1.Duration `json:"approvalTTL,omitempty"`
	// MaxSends bounds the sends of one attempt with the same idempotency
	// key (default 3).
	MaxSends int `json:"maxSends,omitempty"`
	// MaxAttempts bounds the attempts of one proposal (default 1). A new
	// attempt is made only after a definitive Failed result and only when
	// Retryable.
	MaxAttempts int  `json:"maxAttempts,omitempty"`
	Retryable   bool `json:"retryable,omitempty"`
}

// Execution defaults and bounds.
const (
	DefaultApprovalTTL = time.Hour
	DefaultMaxSends    = 3
	DefaultMaxAttempts = 1
	maxApprovers       = 8
	maxImpact          = 8
	maxExecSends       = 5
	maxExecAttempts    = 3
	maxExecDuration    = 24 * time.Hour
)

// HumanDecisionReasons are the declared reasons a person must approve the
// response, in a fixed order; empty means the declared profile is itself the
// policy approval.
func (r Response) HumanDecisionReasons() []string {
	var out []string
	if r.RequiresApproval {
		out = append(out, "approval-declared")
	}
	if len(r.Risks) > 0 {
		out = append(out, "risk-declared")
	}
	if r.Execution != nil && len(r.Execution.MayInterrupt) > 0 {
		out = append(out, "may-interrupt")
	}
	return out
}

// SendBudget is the declared or default send bound of one attempt.
func (e Execution) SendBudget() int {
	if e.MaxSends == 0 {
		return DefaultMaxSends
	}
	return e.MaxSends
}

// AttemptBudget is the declared or default attempt bound.
func (e Execution) AttemptBudget() int {
	if e.MaxAttempts == 0 {
		return DefaultMaxAttempts
	}
	return e.MaxAttempts
}

// ApprovalAge is the declared or default approval TTL.
func (e Execution) ApprovalAge() time.Duration {
	if e.ApprovalTTL.Duration == 0 {
		return DefaultApprovalTTL
	}
	return e.ApprovalTTL.Duration
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
	if r.Execution != nil {
		if err := r.Execution.validate(r); err != nil {
			return fmt.Errorf("execution: %w", err)
		}
	}
	return nil
}

func (e Execution) validate(r Response) error {
	switch {
	case !nameRE.MatchString(e.Provider) || len(e.Provider) > 63:
		return fmt.Errorf("provider %q invalid", e.Provider)
	case len(e.Approvers) > maxApprovers:
		return errors.New("too many approvers")
	case len(e.ExpectedImpact) == 0 || len(e.ExpectedImpact) > maxImpact:
		return fmt.Errorf("expectedImpact needs 1-%d capabilities", maxImpact)
	case len(e.MayInterrupt) > maxImpact:
		return errors.New("too many mayInterrupt capabilities")
	case e.MaxSends < 0 || e.MaxSends > maxExecSends:
		return fmt.Errorf("maxSends must be 0-%d", maxExecSends)
	case e.MaxAttempts < 0 || e.MaxAttempts > maxExecAttempts:
		return fmt.Errorf("maxAttempts must be 0-%d", maxExecAttempts)
	case len(r.HumanDecisionReasons()) > 0 && len(e.Approvers) == 0:
		return errors.New("a response that needs a human decision must declare approvers")
	}
	for _, f := range []struct {
		name string
		d    time.Duration
	}{{"ackTimeout", e.AckTimeout.Duration}, {"completionTimeout", e.CompletionTimeout.Duration}, {"recoveryWindow", e.RecoveryWindow.Duration}} {
		if f.d <= 0 || f.d > maxExecDuration {
			return fmt.Errorf("%s must be within (0, %s]", f.name, maxExecDuration)
		}
	}
	if d := e.ApprovalTTL.Duration; d < 0 || d > maxExecDuration {
		return fmt.Errorf("approvalTTL must be within [0, %s]", maxExecDuration)
	}
	seen := map[string]bool{}
	for _, a := range e.Approvers {
		if a == "" || len(a) > maxOwner || seen[a] {
			return fmt.Errorf("approver %q invalid or repeated", a)
		}
		seen[a] = true
	}
	forIncluded := false
	for i, c := range append(append([]opsv1.CapabilityType{}, e.ExpectedImpact...), e.MayInterrupt...) {
		if err := checkType(c); err != nil {
			return fmt.Errorf("capability %d: %w", i, err)
		}
		forIncluded = forIncluded || (i < len(e.ExpectedImpact) && c == r.For)
	}
	if !forIncluded {
		return errors.New("expectedImpact must include for")
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
