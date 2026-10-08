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
