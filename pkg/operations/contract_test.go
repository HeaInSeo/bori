package operations

import (
	"testing"
	"time"
)

func TestFixtureContractsAreValid(t *testing.T) {
	for _, c := range []Contract{
		httpServiceContract(), resolverContract(), workerContract(), producerContract(),
		replicatedContract(), singleWriterContract(), peerContract(),
	} {
		if errs := c.Validate(); len(errs) != 0 {
			t.Errorf("%s: %v", c.Identity.Name, errs)
		}
	}
}

func TestContractValidationRejectsInvalidPredicates(t *testing.T) {
	enumSlot := AssertionSlot{Name: "mode", Type: TypeString, Enum: []string{"a", "b"}, MaxAge: time.Second}
	cases := map[string]Predicate{
		"IsTrue on Integer":         {Assertion: "n", Op: OpIsTrue},
		"IsFalse on String":         {Assertion: "s", Op: OpIsFalse},
		"IsTrue with operand":       {Assertion: "b", Op: OpIsTrue, Operand: Bool(true)},
		"Gte on Boolean":            {Assertion: "b", Op: OpGte, Operand: Int(1)},
		"Lte on String":             {Assertion: "s", Op: OpLte, Operand: Int(1)},
		"Gte with String operand":   {Assertion: "n", Op: OpGte, Operand: String("1")},
		"Eq type mismatch":          {Assertion: "n", Op: OpEq, Operand: Bool(true)},
		"NotEq type mismatch":       {Assertion: "s", Op: OpNotEq, Operand: Int(0)},
		"Eq operand outside enum":   {Assertion: "mode", Op: OpEq, Operand: String("c")},
		"unknown operator":          {Assertion: "n", Op: "Matches", Operand: String(".*")},
		"undeclared assertion slot": {Assertion: "missing", Op: OpIsTrue},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			c := Contract{
				Identity: contractID("p", "u", "d"),
				Assertions: []AssertionSlot{
					boolSlot("b"), intSlot("n"),
					{Name: "s", Type: TypeString, MaxAge: time.Second},
					enumSlot,
				},
				Capabilities: []Capability{{Type: capType("c"), Requirements: []Requirement{{Predicate: &p, OnUnmet: ImpactDegraded}}}},
			}
			if len(c.Validate()) == 0 {
				t.Fatalf("invalid predicate %+v accepted", p)
			}
			c.Capabilities = nil
			c.Envelopes = []Envelope{{Name: "minimum", Class: EnvelopeMinimum, Requirements: []Predicate{p}}}
			if len(c.Validate()) == 0 {
				t.Fatalf("invalid envelope predicate %+v accepted", p)
			}
		})
	}
}

func TestContractValidationRejectsStructuralDefects(t *testing.T) {
	good := func() Contract { return httpServiceContract() }
	cases := map[string]func(*Contract){
		"incomplete identity":    func(c *Contract) { c.Identity.SpecDigest = "" },
		"unqualified capability": func(c *Contract) { c.Capabilities[0].Type.Domain = "" },
		"duplicate capability":   func(c *Contract) { c.Capabilities = append(c.Capabilities, c.Capabilities[0]) },
		"no requirements":        func(c *Contract) { c.Capabilities[0].Requirements = nil },
		"missing onUnmet":        func(c *Contract) { c.Capabilities[0].Requirements[0].OnUnmet = "" },
		"onUnmet UNKNOWN":        func(c *Contract) { c.Capabilities[0].Requirements[0].OnUnmet = Impact(Unknown) },
		"two kinds in one req":   func(c *Contract) { c.Capabilities[0].Requirements[0].Envelope = "minimum" },
		"empty requirement":      func(c *Contract) { c.Capabilities[0].Requirements[0] = Requirement{OnUnmet: ImpactDegraded} },
		"undeclared dep slot":    func(c *Contract) { c.Capabilities[0].Requirements[0] = dep("nope", "x", ImpactDegraded) },
		"undeclared envelope": func(c *Contract) {
			c.Capabilities[0].Requirements[0] = Requirement{Envelope: "gold", OnUnmet: ImpactDegraded}
		},
		"undeclared local cap": func(c *Contract) {
			c.Capabilities[0].Requirements[0] = Requirement{LocalCapability: "nope", OnUnmet: ImpactDegraded}
		},
		"duplicate slot":         func(c *Contract) { c.Assertions = append(c.Assertions, c.Assertions[0]) },
		"zero maxAge":            func(c *Contract) { c.Assertions[0].MaxAge = 0 },
		"unknown value type":     func(c *Contract) { c.Assertions[0].Type = "Float" },
		"enum on Boolean":        func(c *Contract) { c.Assertions[0].Enum = []string{"x"} },
		"envelope without preds": func(c *Contract) { c.Envelopes = []Envelope{{Name: "minimum", Class: EnvelopeMinimum}} },
		"envelope unknown class": func(c *Contract) {
			c.Envelopes = []Envelope{{Name: "x", Class: "Gold", Requirements: []Predicate{{Assertion: "api-serving", Op: OpIsTrue}}}}
		},
		"unqualified dependency": func(c *Contract) {
			c.DependencySlots = []DependencySlot{{Name: "d"}}
			c.Capabilities[0].Requirements[0] = Requirement{Dependency: &DependencyRequirement{Slot: "d", Capability: CapabilityType{Name: "x"}}, OnUnmet: ImpactDegraded}
		},
		"duplicate dependency slot": func(c *Contract) { c.DependencySlots = []DependencySlot{{Name: "d"}, {Name: "d"}} },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			c := good()
			mut(&c)
			if len(c.Validate()) == 0 {
				t.Fatalf("defect %q accepted", name)
			}
		})
	}
}
