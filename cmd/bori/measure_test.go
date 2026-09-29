package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HeaInSeo/kube-slint/pkg/slint"
	"github.com/HeaInSeo/kube-slint/pkg/slo/fetch"
	"github.com/HeaInSeo/kube-slint/pkg/slo/summary"

	"github.com/HeaInSeo/bori/pkg/verification"
)

// seriesFetcher returns start on the first Fetch and end on every later one.
type seriesFetcher struct {
	start, end map[string]float64
	calls      int
}

func (f *seriesFetcher) Fetch(_ context.Context, at time.Time) (fetch.Sample, error) {
	f.calls++
	if f.calls == 1 {
		return fetch.Sample{At: at, Values: f.start}, nil
	}
	return fetch.Sample{At: at, Values: f.end}, nil
}

// operatorSeries is a bori-operator snapshot with the label sets
// controller-runtime exports (taken from the lab VM operator).
func operatorSeries(reconcileSuccess, rest503 float64, extra map[string]float64) map[string]float64 {
	m := map[string]float64{
		`controller_runtime_reconcile_errors_total{controller="boridataplane"}`:                 0,
		`controller_runtime_reconcile_errors_total{controller="borirelease"}`:                   0,
		`controller_runtime_reconcile_total{controller="boridataplane",result="error"}`:         0,
		`controller_runtime_reconcile_total{controller="boridataplane",result="requeue"}`:       0,
		`controller_runtime_reconcile_total{controller="boridataplane",result="requeue_after"}`: 0,
		`controller_runtime_reconcile_total{controller="boridataplane",result="success"}`:       reconcileSuccess,
		`controller_runtime_reconcile_total{controller="borirelease",result="success"}`:         7,
		`rest_client_requests_total{code="200",host="10.96.0.1:443",method="GET"}`:              5,
		`workqueue_depth{controller="boridataplane",name="boridataplane",priority="-100"}`:      0,
		`workqueue_depth{controller="borirelease",name="borirelease",priority="-100"}`:          3,
	}
	if rest503 > 0 {
		m[`rest_client_requests_total{code="503",host="10.96.0.1:443",method="PUT"}`] = rest503
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// useFetcher makes newMeasurementSession build a real kube-slint session that
// reads from f instead of a curl pod.
func useFetcher(t *testing.T, f fetch.MetricsFetcher) *slint.SessionConfig {
	t.Helper()
	t.Setenv("SLINT_DISABLE_DISCOVERY", "1")
	var seen slint.SessionConfig
	orig := newMeasurementSession
	newMeasurementSession = func(cfg slint.SessionConfig) measurementSession {
		seen = cfg
		cfg.Fetcher = f
		return slint.NewSession(cfg)
	}
	t.Cleanup(func() { newMeasurementSession = orig })
	return &seen
}

func testRequest(t *testing.T) measureRequest {
	t.Helper()
	p, err := lookupProfile("bori-operator")
	if err != nil {
		t.Fatal(err)
	}
	return measureRequest{
		Profile:     p,
		RunID:       "run-1",
		SubjectID:   "bori-operator@lab-vm",
		WindowID:    "vm-smoke/fixture-reconcile/v1",
		EvidenceDir: filepath.Join(t.TempDir(), "evidence"),
	}
}

func noSmoke(context.Context) error { return nil }

func TestMeasureAround_ProducesTrustCorrectSummary(t *testing.T) {
	f := &seriesFetcher{start: operatorSeries(0, 0, nil), end: operatorSeries(2, 1, nil)}
	seen := useFetcher(t, f)
	req := testRequest(t)

	path, err := measureAround(context.Background(), req, noSmoke)
	if err != nil {
		t.Fatalf("measureAround: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("summary not written: %v", err)
	}
	if seen.TrustContract == nil || seen.TrustContract.SubjectID != req.SubjectID ||
		seen.TrustContract.WindowID != req.WindowID || seen.TrustContract.SourceConfigID != req.Profile.SourceConfigID {
		t.Fatalf("TrustContract not forwarded verbatim: %+v", seen.TrustContract)
	}
	if seen.Namespace != "bori-system" || seen.MetricsServiceName != "bori-operator-metrics" || len(seen.Specs) != 4 {
		t.Fatalf("session config = %+v", seen)
	}

	sum, err := summary.LoadFile(path)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if sum.SchemaVersion != summary.SchemaVersionTrust {
		t.Fatalf("schema = %s", sum.SchemaVersion)
	}
	want := map[string]float64{
		"reconcile-delta":        2,
		"reconcile-errors-delta": 0,
		"workqueue-depth-end":    0,
		"rest-errors-delta":      1,
	}
	for _, r := range sum.Results {
		if r.Value == nil || *r.Value != want[r.ID] {
			t.Errorf("%s = %v, want %v", r.ID, r.Value, want[r.ID])
		}
	}
}

func TestMeasureAround_FailsClosed(t *testing.T) {
	cases := map[string]struct {
		start, end map[string]float64
		want       string
	}{
		"ambiguous workqueue series": {
			start: operatorSeries(0, 0, nil),
			end: operatorSeries(1, 0, map[string]float64{
				`workqueue_depth{controller="boridataplane",name="boridataplane",priority="0"}`: 1,
			}),
			want: "workqueue-depth-end skipped",
		},
		"rest family absent": {
			start: without(operatorSeries(0, 0, nil), "rest_client_requests_total"),
			end:   without(operatorSeries(1, 0, nil), "rest_client_requests_total"),
			want:  "rest-errors-delta skipped",
		},
		"reconcile series missing": {
			start: without(operatorSeries(0, 0, nil), "controller_runtime_reconcile_total"),
			end:   operatorSeries(1, 0, nil),
			want:  "reconcile-delta skipped",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			useFetcher(t, &seriesFetcher{start: tc.start, end: tc.end})
			req := testRequest(t)
			path, err := measureAround(context.Background(), req, noSmoke)
			if err == nil {
				t.Fatalf("want error, got summary %s", path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func without(m map[string]float64, family string) map[string]float64 {
	out := map[string]float64{}
	for k, v := range m {
		if !strings.HasPrefix(k, family+"{") {
			out[k] = v
		}
	}
	return out
}

// fakeSession records calls and returns a canned End result.
type fakeSession struct {
	started, ended bool
	sum            *summary.Summary
	err            error
}

func (s *fakeSession) Start() { s.started = true }
func (s *fakeSession) End(context.Context) (*summary.Summary, error) {
	s.ended = true
	return s.sum, s.err
}

func useSession(t *testing.T, s *fakeSession) {
	t.Helper()
	orig := newMeasurementSession
	newMeasurementSession = func(slint.SessionConfig) measurementSession { return s }
	t.Cleanup(func() { newMeasurementSession = orig })
}

func TestMeasureAround_RequiresTrustContractCoordinates(t *testing.T) {
	for _, blank := range []string{"subject", "window"} {
		t.Run(blank, func(t *testing.T) {
			s := &fakeSession{}
			useSession(t, s)
			req := testRequest(t)
			if blank == "subject" {
				req.SubjectID = " "
			} else {
				req.WindowID = ""
			}
			if _, err := measureAround(context.Background(), req, noSmoke); !errors.Is(err, errNoTrustContract) {
				t.Fatalf("err = %v, want errNoTrustContract", err)
			}
			if s.started {
				t.Fatal("session started without a TrustContract")
			}
		})
	}
}

func TestMeasureAround_StartBeforeSmokeEndAfter(t *testing.T) {
	s := &fakeSession{err: errors.New("stop")}
	useSession(t, s)
	var order []string
	smoke := func(context.Context) error {
		order = append(order, "smoke")
		if !s.started || s.ended {
			t.Errorf("smoke ran with started=%v ended=%v", s.started, s.ended)
		}
		return nil
	}
	_, _ = measureAround(context.Background(), testRequest(t), smoke)
	if len(order) != 1 || !s.ended {
		t.Fatalf("smoke calls = %v, ended = %v", order, s.ended)
	}
}

func TestMeasureAround_SmokeFailureSkipsEnd(t *testing.T) {
	s := &fakeSession{}
	useSession(t, s)
	_, err := measureAround(context.Background(), testRequest(t), func(context.Context) error {
		return errors.New("fixture apply failed")
	})
	if err == nil || !strings.Contains(err.Error(), "smoke") {
		t.Fatalf("err = %v", err)
	}
	if s.ended {
		t.Fatal("End ran after a failed smoke")
	}
}

func TestMeasureAround_RemovesStaleSummary(t *testing.T) {
	s := &fakeSession{err: errors.New("producer failed")}
	useSession(t, s)
	req := testRequest(t)
	if err := os.MkdirAll(req.EvidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(req.EvidenceDir, measurementSummaryFile)
	if err := os.WriteFile(stale, []byte(`{"schemaVersion":"slo.v4"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := measureAround(context.Background(), req, noSmoke); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale summary still present: %v", err)
	}
}

func TestMeasureAround_ProducerReturnsSummaryButNoFile(t *testing.T) {
	req := testRequest(t)
	s := &fakeSession{sum: completeSummary(req)}
	useSession(t, s)
	if _, err := measureAround(context.Background(), req, noSmoke); err == nil || !strings.Contains(err.Error(), "wrote no summary") {
		t.Fatalf("err = %v", err)
	}
}

func completeSummary(req measureRequest) *summary.Summary {
	sum := &summary.Summary{SchemaVersion: summary.SchemaVersionTrust}
	for _, s := range req.Profile.Specs() {
		v := 1.0
		sum.Results = append(sum.Results, summary.SLIResult{
			ID:     s.ID,
			Value:  &v,
			Status: summary.StatusPass,
			Comparability: &summary.Comparability{
				SLIContractID:  "c-" + s.ID,
				SubjectID:      req.SubjectID,
				SourceConfigID: req.Profile.SourceConfigID,
				WindowID:       req.WindowID,
			},
		})
	}
	return sum
}

func TestValidateSummary(t *testing.T) {
	req := testRequest(t)
	if err := validateSummary(completeSummary(req), req); err != nil {
		t.Fatalf("complete summary: %v", err)
	}
	cases := map[string]func(*summary.Summary){
		"legacy schema":         func(s *summary.Summary) { s.SchemaVersion = summary.SchemaVersionLegacy },
		"missing result":        func(s *summary.Summary) { s.Results = s.Results[1:] },
		"unexpected result":     func(s *summary.Summary) { s.Results = append(s.Results, summary.SLIResult{ID: "other"}) },
		"duplicate result":      func(s *summary.Summary) { s.Results = append(s.Results, s.Results[0]) },
		"skipped result":        func(s *summary.Summary) { s.Results[0].Status = summary.StatusSkip },
		"nil value":             func(s *summary.Summary) { s.Results[1].Value = nil },
		"no comparability":      func(s *summary.Summary) { s.Results[2].Comparability = nil },
		"foreign subject":       func(s *summary.Summary) { s.Results[3].Comparability.SubjectID = "other" },
		"blank window":          func(s *summary.Summary) { s.Results[3].Comparability.WindowID = " " },
		"foreign source config": func(s *summary.Summary) { s.Results[0].Comparability.SourceConfigID = "other" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := completeSummary(req)
			mutate(s)
			if err := validateSummary(s, req); err == nil {
				t.Fatal("want error")
			}
		})
	}
	if err := validateSummary(nil, req); err == nil {
		t.Fatal("nil summary: want error")
	}
}

// recordingProvider records gate calls.
type recordingProvider struct {
	calls []verification.Request
}

func (p *recordingProvider) Run(_ context.Context, req verification.Request) (*verification.Result, error) {
	p.calls = append(p.calls, req)
	return &verification.Result{App: req.App, GateResult: verification.GateResultPass}, nil
}

func testTarget(name, policyPath string) verifyTarget {
	return verifyTarget{Name: name, Policies: []resolvedPolicy{{
		Name: "slint", PolicyPath: policyPath, FailOn: verification.FailOnFailOrNoGrade, Blocking: true,
	}}}
}

func writePolicy(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(p, []byte("gate:\n  fail_on: NONE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var testOpts = measureOptions{SubjectID: "bori-operator@lab-vm", WindowID: "vm-smoke/fixture-reconcile/v1"}

func logNone(string, ...any) {}

func TestRunOneVerification_UnknownTargetIsNoGradeWithoutGate(t *testing.T) {
	s := &fakeSession{}
	useSession(t, s)
	prov := &recordingProvider{}
	cs, gr, blocked := runOneVerification(context.Background(), testTarget("jumi", writePolicy(t)), "run-1",
		t.TempDir(), "", 0, testOpts, prov, logNone)
	if gr != verification.GateResultNoGrade || cs.GateResult != string(verification.GateResultNoGrade) || !blocked {
		t.Fatalf("result = %s / %s, blocked = %v", gr, cs.GateResult, blocked)
	}
	if s.started || len(prov.calls) != 0 {
		t.Fatalf("measurement started=%v, gate calls=%d", s.started, len(prov.calls))
	}
}

func TestRunOneVerification_IncompleteMeasurementIsNoGradeWithoutGate(t *testing.T) {
	useFetcher(t, &seriesFetcher{
		start: without(operatorSeries(0, 0, nil), "rest_client_requests_total"),
		end:   without(operatorSeries(1, 0, nil), "rest_client_requests_total"),
	})
	prov := &recordingProvider{}
	_, gr, blocked := runOneVerification(context.Background(), testTarget("bori-operator", writePolicy(t)), "run-1",
		t.TempDir(), "", 0, testOpts, prov, logNone)
	if gr != verification.GateResultNoGrade || !blocked {
		t.Fatalf("result = %s, blocked = %v; want NO_GRADE, blocked", gr, blocked)
	}
	if len(prov.calls) != 0 {
		t.Fatalf("gate ran on an incomplete measurement: %+v", prov.calls)
	}
}

func TestRunOneVerification_SmokeFailureIsFail(t *testing.T) {
	useSession(t, &fakeSession{})
	prov := &recordingProvider{}
	_, gr, blocked := runOneVerification(context.Background(), testTarget("bori-operator", writePolicy(t)), "run-1",
		t.TempDir(), "exit 3", 0, testOpts, prov, logNone)
	if gr != verification.GateResultFail || !blocked || len(prov.calls) != 0 {
		t.Fatalf("result = %s, blocked = %v, gate calls = %d", gr, blocked, len(prov.calls))
	}
}

func TestRunOneVerification_GateReadsProducerSummary(t *testing.T) {
	useFetcher(t, &seriesFetcher{start: operatorSeries(0, 0, nil), end: operatorSeries(1, 0, nil)})
	prov := &recordingProvider{}
	runDir := t.TempDir()
	_, gr, _ := runOneVerification(context.Background(), testTarget("bori-operator", writePolicy(t)), "run-1",
		runDir, "", 0, testOpts, prov, logNone)
	if gr != verification.GateResultPass {
		t.Fatalf("result = %s", gr)
	}
	if len(prov.calls) != 1 {
		t.Fatalf("gate calls = %d", len(prov.calls))
	}
	want := filepath.Join(runDir, "evidence", "bori-operator", measurementSummaryFile)
	if got := prov.calls[0].MeasurementSummaryPath; got != want {
		t.Fatalf("gate summary = %s, want %s", got, want)
	}
	sum, err := summary.LoadFile(want)
	if err != nil || sum.SchemaVersion != summary.SchemaVersionTrust {
		t.Fatalf("summary at gate time: %v / %+v", err, sum)
	}
}

// noGradeProvider returns NO_GRADE, like slint-gate on a summary-only policy.
type noGradeProvider struct{ calls int }

func (p *noGradeProvider) Run(_ context.Context, req verification.Request) (*verification.Result, error) {
	p.calls++
	return &verification.Result{App: req.App, GateResult: verification.GateResultNoGrade}, nil
}

func TestRunOneVerification_FailOnNeverGateDoesNotHalt(t *testing.T) {
	useFetcher(t, &seriesFetcher{start: operatorSeries(0, 0, nil), end: operatorSeries(1, 0, nil)})
	prov := &noGradeProvider{}
	tgt := testTarget("bori-operator", writePolicy(t))
	tgt.Policies[0].FailOn = verification.FailOnNever
	_, gr, blocked := runOneVerification(context.Background(), tgt, "run-1", t.TempDir(), "", 0, testOpts, prov, logNone)
	if prov.calls != 1 || gr != verification.GateResultNoGrade || blocked {
		t.Fatalf("gate calls = %d, result = %s, blocked = %v", prov.calls, gr, blocked)
	}
}
