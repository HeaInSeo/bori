package interaction

import (
	"strings"
	"testing"
)

func TestProfileValidation(t *testing.T) {
	ok := `{"revision":"p1","responses":[{"action":{"name":"remount","revision":"r1"},
	  "target":{"namespace":"apps","name":"y"},"for":{"domain":"example.io","name":"persist","revision":"v1"},
	  "states":["UNAVAILABLE"],"owner":"storage-oncall","requiresApproval":true,"risks":["data-loss"],
	  "preconditions":[{"domain":"example.io","name":"serve","revision":"v1"}]}]}`
	if _, err := ParseProfile([]byte(ok)); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"unknown field":   strings.Replace(ok, `"owner"`, `"executeCommand":"x","owner"`, 1),
		"no revision":     strings.Replace(ok, `"revision":"p1"`, `"revision":""`, 1),
		"unknown risk":    strings.Replace(ok, `"data-loss"`, `"whatever"`, 1),
		"bad state":       strings.Replace(ok, `["UNAVAILABLE"]`, `["AVAILABLE"]`, 1),
		"no target":       strings.Replace(ok, `"name":"y"`, `"name":""`, 1),
		"partial for":     strings.Replace(ok, `"name":"persist","revision":"v1"`, `"name":"persist","revision":""`, 1),
		"bad action name": strings.Replace(ok, `"name":"remount"`, `"name":"Remount Now"`, 1),
	}
	for name, doc := range bad {
		if _, err := ParseProfile([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var many strings.Builder
	many.WriteString(`{"revision":"p1","responses":[`)
	for i := 0; i <= MaxResponses; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`{"action":{"name":"a","revision":"r1"},"target":{"namespace":"n","name":"t"},"for":{"domain":"d","name":"c","revision":"v1"}}`)
	}
	many.WriteString(`]}`)
	if _, err := ParseProfile([]byte(many.String())); err == nil {
		t.Error("unbounded profile accepted")
	}
}

func TestExecutionValidation(t *testing.T) {
	ok := `{"revision":"p1","responses":[{"action":{"name":"remount","revision":"r1"},
	  "target":{"namespace":"apps","name":"y"},"for":{"domain":"example.io","name":"persist","revision":"v1"},
	  "owner":"storage-oncall","requiresApproval":true,
	  "execution":{"provider":"storage-actor","approvers":["alice"],
	    "expectedImpact":[{"domain":"example.io","name":"persist","revision":"v1"}],
	    "ackTimeout":"10s","completionTimeout":"5m","recoveryWindow":"2m","maxSends":3,"maxAttempts":2,"retryable":true}}]}`
	p, err := ParseProfile([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if e := p.Responses[0].Execution; e == nil || e.SendBudget() != 3 || e.AttemptBudget() != 2 || e.ApprovalAge() != DefaultApprovalTTL {
		t.Fatalf("%+v", e)
	}
	bad := map[string]string{
		"unknown field":            strings.Replace(ok, `"provider"`, `"command":"rm -rf","provider"`, 1),
		"no approvers":             strings.Replace(ok, `"approvers":["alice"],`, ``, 1),
		"repeated approver":        strings.Replace(ok, `["alice"]`, `["alice","alice"]`, 1),
		"impact without for":       strings.Replace(ok, `"expectedImpact":[{"domain":"example.io","name":"persist"`, `"expectedImpact":[{"domain":"example.io","name":"serve"`, 1),
		"no impact":                strings.Replace(ok, `"expectedImpact":[{"domain":"example.io","name":"persist","revision":"v1"}]`, `"expectedImpact":[]`, 1),
		"bad provider":             strings.Replace(ok, `"storage-actor"`, `"Storage Actor"`, 1),
		"zero timeout":             strings.Replace(ok, `"10s"`, `"0s"`, 1),
		"huge window":              strings.Replace(ok, `"2m"`, `"48h"`, 1),
		"too many sends":           strings.Replace(ok, `"maxSends":3`, `"maxSends":9`, 1),
		"too many attempts":        strings.Replace(ok, `"maxAttempts":2`, `"maxAttempts":4`, 1),
		"oversized impact":         strings.Replace(ok, `"expectedImpact":[{"domain":"example.io"`, `"expectedImpact":[{"domain":"`+strings.Repeat("d", 254)+`"`, 1),
		"interrupt without person": strings.Replace(strings.Replace(ok, `"requiresApproval":true,`, ``, 1), `"approvers":["alice"],`, `"mayInterrupt":[{"domain":"example.io","name":"serve","revision":"v1"}],`, 1),
	}
	for name, doc := range bad {
		if _, err := ParseProfile([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	policy := strings.Replace(strings.Replace(ok, `"requiresApproval":true,`, ``, 1), `"approvers":["alice"],`, ``, 1)
	if p, err := ParseProfile([]byte(policy)); err != nil || len(p.Responses[0].HumanDecisionReasons()) != 0 {
		t.Fatalf("policy-approved response rejected: %v", err)
	}
}
