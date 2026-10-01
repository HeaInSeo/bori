package operations

// ContractIdentity is the exact semantic identity of an OperationalContract.
// Namespace/name alone is not identity: a contract deleted and recreated under
// the same name, or edited in place, has a different UID or SpecDigest and is
// a different contract.
type ContractIdentity struct {
	Namespace  string
	Name       string
	UID        string
	SpecDigest string
}

func (c ContractIdentity) complete() bool {
	return c.Namespace != "" && c.Name != "" && c.UID != "" && c.SpecDigest != ""
}

func (c ContractIdentity) String() string {
	return c.Namespace + "/" + c.Name + "@" + c.UID + ":" + c.SpecDigest
}

// TargetIdentity is the identity of an OperationalTarget. UID distinguishes a
// recreated target from its predecessor with the same name.
type TargetIdentity struct {
	Namespace string
	Name      string
	UID       string
}

func (t TargetIdentity) String() string {
	return t.Namespace + "/" + t.Name + "@" + t.UID
}

// CapabilityType is the qualified semantic identity of a capability. The
// display name alone never identifies a capability across contracts: two
// unrelated contracts may both call something "resolve".
type CapabilityType struct {
	Domain   string
	Name     string
	Revision string
}

func (c CapabilityType) complete() bool {
	return c.Domain != "" && c.Name != "" && c.Revision != ""
}

func (c CapabilityType) String() string {
	return c.Domain + "/" + c.Name + "@" + c.Revision
}

func (c CapabilityType) less(o CapabilityType) bool {
	if c.Domain != o.Domain {
		return c.Domain < o.Domain
	}
	if c.Name != o.Name {
		return c.Name < o.Name
	}
	return c.Revision < o.Revision
}

// ProviderIdentity identifies the evidence provider bound to an assertion
// slot, including the revision of its configuration. A config change makes
// evidence produced under the previous revision inapplicable.
type ProviderIdentity struct {
	Name           string
	ConfigRevision string
}

func (p ProviderIdentity) String() string {
	return p.Name + "@" + p.ConfigRevision
}

// ApplicabilityKey is the exact binding an observation was produced for. An
// observation is applicable only when every element equals the element the
// evaluator expects for that target and slot; otherwise it is INAPPLICABLE and
// never silently carried over.
//
// Target carries the OperationalTarget namespace/name so that evidence about a
// replaced instance can be attributed; applicability still requires the UID.
type ApplicabilityKey struct {
	Target      TargetIdentity
	ResolvedUID string
	Contract    ContractIdentity
	Slot        string
	Provider    ProviderIdentity
}
