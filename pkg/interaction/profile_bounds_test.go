package interaction

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/investigate"
	"github.com/HeaInSeo/bori/pkg/operations"
	"github.com/HeaInSeo/bori/pkg/opswire"
	"github.com/HeaInSeo/bori/pkg/providers"
)

// loadProfileOf writes p as a profile file and loads it through the
// operator's path.
func loadProfileOf(t *testing.T, p Profile) (*Profile, error) {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadProfile(path)
}

func maxType(c byte) opsv1.CapabilityType {
	label := strings.Repeat(string(c), 63)
	return opsv1.CapabilityType{
		Domain:   strings.Join([]string{label, label, label, strings.Repeat(string(c), 61)}, "."), // 253
		Name:     label,
		Revision: label,
	}
}

// Codex r4228097338 regression: each CapabilityType field of response.for
// and of every precondition is held to the API bounds (domain 1–253, name
// 1–63, revision 1–63) at profile load, so no oversized key can reach a
// status subject.
func TestProfileCapabilityTypeBounds(t *testing.T) {
	ok := opsv1.CapabilityType{Domain: "storage.example", Name: "persist", Revision: "v1"}
	bad := map[string]opsv1.CapabilityType{
		"domain254":           {Domain: strings.Repeat("d", 254), Name: "x", Revision: "1"},
		"name64":              {Domain: "d", Name: strings.Repeat("n", 64), Revision: "1"},
		"revision64":          {Domain: "d", Name: "x", Revision: strings.Repeat("r", 64)},
		"codex domain513":     {Domain: strings.Repeat("d", 513), Name: "x", Revision: "1"},
		"empty domain":        {Name: "x", Revision: "1"},
		"empty name":          {Domain: "d", Revision: "1"},
		"empty revision":      {Domain: "d", Name: "x"},
		"domain254 multibyte": {Domain: strings.Repeat("é", 254), Name: "x", Revision: "1"},
	}
	base := func() Response { r := resp("remount", "storage-oncall", "persist"); r.For = ok; return r }
	for name, ct := range bad {
		t.Run(name, func(t *testing.T) {
			pre := base()
			pre.Preconditions = []opsv1.CapabilityType{ok, ct}
			forR := base()
			forR.For = ct
			for where, r := range map[string]Response{"precondition": pre, "for": forR} {
				p := Profile{Revision: "p1", Responses: []Response{r}}
				if _, err := loadProfileOf(t, p); err == nil {
					t.Errorf("%s %s: LoadProfile accepted it", where, name)
				}
				if err := p.Validate(); err == nil {
					t.Errorf("%s %s: Validate accepted it", where, name)
				}
			}
		})
	}

	// Control: the API maxima (253/63/63, key 381) load in both positions.
	r := base()
	r.For = maxType('a')
	r.Preconditions = []opsv1.CapabilityType{maxType('b'), {Domain: "d", Name: "x", Revision: "1"}}
	if _, err := loadProfileOf(t, Profile{Revision: "p1", Responses: []Response{r}}); err != nil {
		t.Fatalf("API-maximum capability types rejected: %v", err)
	}
	// Multibyte characters count as characters, as the API server counts them.
	r.Preconditions = []opsv1.CapabilityType{{Domain: strings.Repeat("é", 253), Name: strings.Repeat("ñ", 63), Revision: strings.Repeat("ü", 63)}}
	if _, err := loadProfileOf(t, Profile{Revision: "p1", Responses: []Response{r}}); err != nil {
		t.Fatalf("253/63/63-character multibyte type rejected: %v", err)
	}
}

// The valid maximum: a loaded profile whose matching response has 381-character
// precondition keys projects to a summary the generated CRD schema accepts.
func TestMaximumPreconditionKeysFitTheSchema(t *testing.T) {
	ct, other := maxType('a'), maxType('b')
	slot, env := "slot", "env"
	five := int64(5)
	spec := opsv1.OperationalContractSpec{
		Assertions:   []opsv1.AssertionSlot{intSlot(slot)},
		Capabilities: []opsv1.Capability{{Type: ct, Requirements: []opsv1.Requirement{{Envelope: env, OnUnmet: "DEGRADED"}}}},
		Envelopes: []opsv1.Envelope{{Name: env, Class: "Recommended", Requirements: []opsv1.Predicate{
			{Assertion: slot, Operator: "Gte", Operand: &opsv1.Operand{Integer: &five}},
		}}},
	}
	c := contract("apps", "max-v1", "c-max", spec)
	tg := target("apps", "max", "t-max", "max-v1", bind(slot, "kube-status"))
	res := map[types.NamespacedName]opswire.Resolution{{Namespace: "apps", Name: "max"}: {UID: "w-max"}}
	b := opswire.Build(opswire.Input{At: at, Contracts: []opsv1.OperationalContract{c}, Targets: []opsv1.OperationalTarget{tg}, Resolutions: res})
	subjects := map[string]providers.Subject{"t-max": {Namespace: "apps", APIVersion: "apps/v1", Kind: "Deployment", Name: "max", ResolvedUID: "w-max"}}
	iv := investigate.New(investigate.ReferenceLimits(), func() time.Time { return at })
	iv.Run(context.Background(), b.Snapshot, investigate.Requests(b.Snapshot, subjects, oneLookup{constProvider{1}}))
	b.Snapshot.Observations = iv.Observations()
	a, err := operations.Evaluate(b.Snapshot)
	if err != nil {
		t.Fatal(err)
	}

	r := Response{
		Action: ActionRef{Name: "remount", Revision: "r1"}, Target: TargetRef{Namespace: "apps", Name: "max"},
		For: ct, Owner: "storage-oncall", Preconditions: []opsv1.CapabilityType{ct, other},
	}
	p, err := loadProfileOf(t, Profile{Revision: "p1", Responses: []Response{r}})
	if err != nil {
		t.Fatal(err)
	}
	s := Project(Input{Namespace: "apps", Name: "max", UID: "t-max", Status: opswire.Project(&tg, b, a), Profile: p}, nil)
	if len(s.Responses) != 1 || len(s.Responses[0].Preconditions) != 2 {
		t.Fatalf("responses %+v", s.Responses)
	}
	for _, pc := range s.Responses[0].Preconditions {
		if len(pc.Subject) != 381 {
			t.Fatalf("precondition subject %d characters, want the unchanged 381-character key", len(pc.Subject))
		}
	}
	validateSummary(t, s)
}
