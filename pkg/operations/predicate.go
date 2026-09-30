package operations

import "fmt"

// ValueType is the declared type of an assertion slot. Values are never
// coerced between types.
type ValueType string

const (
	TypeBoolean ValueType = "Boolean"
	TypeInteger ValueType = "Integer"
	// TypeString covers free strings and, when the slot declares Enum values,
	// closed enumerations.
	TypeString ValueType = "String"
)

// Value is a typed assertion value.
type Value struct {
	Type ValueType
	Bool bool
	Int  int64
	Str  string
}

func Bool(b bool) Value     { return Value{Type: TypeBoolean, Bool: b} }
func Int(i int64) Value     { return Value{Type: TypeInteger, Int: i} }
func String(s string) Value { return Value{Type: TypeString, Str: s} }

func (v Value) String() string {
	switch v.Type {
	case TypeBoolean:
		return fmt.Sprintf("%t", v.Bool)
	case TypeInteger:
		return fmt.Sprintf("%d", v.Int)
	case TypeString:
		return fmt.Sprintf("%q", v.Str)
	}
	return "<invalid>"
}

// Operator is one of the bounded typed predicate operators. O1 deliberately
// has no general expression language.
type Operator string

const (
	OpIsTrue  Operator = "IsTrue"
	OpIsFalse Operator = "IsFalse"
	OpEq      Operator = "Eq"
	OpNotEq   Operator = "NotEq"
	OpGte     Operator = "Gte"
	OpLte     Operator = "Lte"
)

// Predicate tests one assertion slot of the same target.
type Predicate struct {
	Assertion string
	Op        Operator
	// Operand is required for Eq/NotEq/Gte/Lte and must be empty for
	// IsTrue/IsFalse.
	Operand Value
}

// validate checks the type/operator pairing against the declared slot.
func (p Predicate) validate(slots map[string]AssertionSlot) error {
	slot, ok := slots[p.Assertion]
	if !ok {
		return fmt.Errorf("predicate references undeclared assertion %q", p.Assertion)
	}
	switch p.Op {
	case OpIsTrue, OpIsFalse:
		if slot.Type != TypeBoolean {
			return fmt.Errorf("%s on %s assertion %q", p.Op, slot.Type, p.Assertion)
		}
		if p.Operand != (Value{}) {
			return fmt.Errorf("%s on %q takes no operand", p.Op, p.Assertion)
		}
	case OpEq, OpNotEq:
		if p.Operand.Type != slot.Type {
			return fmt.Errorf("%s operand type %q does not match %s assertion %q", p.Op, p.Operand.Type, slot.Type, p.Assertion)
		}
		if !slot.admits(p.Operand) {
			return fmt.Errorf("%s operand %s is not a declared enum value of %q", p.Op, p.Operand, p.Assertion)
		}
	case OpGte, OpLte:
		if slot.Type != TypeInteger {
			return fmt.Errorf("%s on %s assertion %q", p.Op, slot.Type, p.Assertion)
		}
		if p.Operand.Type != TypeInteger {
			return fmt.Errorf("%s operand type %q on Integer assertion %q", p.Op, p.Operand.Type, p.Assertion)
		}
	default:
		return fmt.Errorf("unknown operator %q on %q", p.Op, p.Assertion)
	}
	return nil
}

// eval applies a validated predicate to a value already checked against the
// slot type.
func (p Predicate) eval(v Value) bool {
	switch p.Op {
	case OpIsTrue:
		return v.Bool
	case OpIsFalse:
		return !v.Bool
	case OpEq:
		return v == p.Operand
	case OpNotEq:
		return v != p.Operand
	case OpGte:
		return v.Int >= p.Operand.Int
	case OpLte:
		return v.Int <= p.Operand.Int
	}
	return false
}

func (p Predicate) String() string {
	switch p.Op {
	case OpIsTrue, OpIsFalse:
		return fmt.Sprintf("%s %s", p.Assertion, p.Op)
	}
	return fmt.Sprintf("%s %s %s", p.Assertion, p.Op, p.Operand)
}
