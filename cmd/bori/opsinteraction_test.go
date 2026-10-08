package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const listJSON = `{"apiVersion":"v1","kind":"List","items":[
 {"kind":"OperationalTarget","metadata":{"namespace":"apps","name":"y","uid":"t-y"},
  "spec":{"targetRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"y"},"contractRef":{"name":"service-v1"}},
  "status":{"valid":true,"interaction":{"level":"DECISION_REQUIRED","summary":"1 affected (persist UNAVAILABLE); 2 AVAILABLE on current evidence",
   "levelReasons":[{"code":"approval-required","subject":"example.io/persist@v1"}],
   "affected":[{"type":{"domain":"example.io","name":"persist","revision":"v1"},"state":"UNAVAILABLE","reasons":[{"code":"predicate-unmet"}]}],
   "responses":[{"name":"failover@r1","target":"apps/y#t-y","for":{"domain":"example.io","name":"persist","revision":"v1"},"owner":"storage-oncall","requiresApproval":true,"status":"ApprovalRequired"}],
   "humanReasons":[{"code":"approval-required","subject":"failover@r1"}],
   "pendingPostConditions":[{"code":"capability-available-on-current-evidence","subject":"example.io/persist@v1","detail":"unconfirmed"}],
   "fingerprint":"f1","recentFingerprints":["f1"]}}},
 {"kind":"OperationalTarget","metadata":{"namespace":"apps","name":"z","uid":"t-z"},"spec":{"targetRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"z"},"contractRef":{"name":"c"}},"status":{}}]}`

func TestOpsInteractionText(t *testing.T) {
	var out bytes.Buffer
	if err := runOpsInteraction(nil, strings.NewReader(listJSON), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"2 targets: IMMEDIATE_INTERVENTION=0 DECISION_REQUIRED=1 AWARENESS=0 NO_ACTION=0, without summary=1 (counts only; no overall health)",
		"apps/y", "level:      DECISION_REQUIRED", "failover@r1 for persist on apps/y#t-y, owner storage-oncall: ApprovalRequired (display only)",
		"human:      approval-required failover@r1", "pending:    capability-available-on-current-evidence example.io/persist@v1 (unconfirmed)",
		"apps/z\n  no interaction summary",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestOpsInteractionJSONAndEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := runOpsInteraction([]string{"-o", "json"}, strings.NewReader(listJSON), &out); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Overview struct {
			Targets         int            `json:"targets"`
			NothingAssessed bool           `json:"nothingAssessed"`
			ByLevel         map[string]int `json:"byLevel"`
		} `json:"overview"`
		Targets []struct {
			Name        string          `json:"name"`
			Interaction json.RawMessage `json:"interaction"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Overview.Targets != 2 || doc.Overview.ByLevel["DECISION_REQUIRED"] != 1 || string(doc.Targets[1].Interaction) != "null" {
		t.Fatalf("%s", out.String())
	}

	out.Reset()
	if err := runOpsInteraction(nil, strings.NewReader(`{"kind":"List","items":[]}`), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing is assessed (this is not NO_ACTION)") {
		t.Fatalf("%q", out.String())
	}
	if err := runOpsInteraction(nil, strings.NewReader(`{"kind":"List","items":[{"kind":"Secret"}]}`), &out); err == nil {
		t.Fatal("non-target item accepted")
	}
}
