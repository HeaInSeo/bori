package release

import (
	"testing"

	"github.com/HeaInSeo/bori/pkg/model"
)

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		ver, min string
		want     bool
	}{
		{"v0.3.0", "v0.2.0", true},
		{"v0.2.0", "v0.2.0", true},
		{"v0.1.5", "v0.2.0", false},
		{"v1.0.0", "v0.9.9", true},
		{"v0.3.0", "v0.3.1", false},
	}
	for _, c := range cases {
		got := versionAtLeast(c.ver, c.min)
		if got != c.want {
			t.Errorf("versionAtLeast(%q, %q) = %v; want %v", c.ver, c.min, got, c.want)
		}
	}
}

func TestCheckCompatibility(t *testing.T) {
	matrix := CompatibilityMatrix{
		Constraints: []Constraint{
			{
				Component: "jumi",
				Version:   "v0.3.0",
				Requires: []VersionRequirement{
					{Component: "artifact-handoff", MinVersion: "v0.2.0"},
				},
			},
		},
	}

	t.Run("compatible", func(t *testing.T) {
		rel := model.BoriRelease{
			Components: []model.ComponentRef{
				{Name: "jumi", Version: "v0.3.0"},
				{Name: "artifact-handoff", Version: "v0.2.0"},
			},
		}
		violations := CheckCompatibility(rel, matrix)
		if len(violations) != 0 {
			t.Errorf("expected no violations, got %v", violations)
		}
	})

	t.Run("version too low", func(t *testing.T) {
		rel := model.BoriRelease{
			Components: []model.ComponentRef{
				{Name: "jumi", Version: "v0.3.0"},
				{Name: "artifact-handoff", Version: "v0.1.0"},
			},
		}
		violations := CheckCompatibility(rel, matrix)
		if len(violations) != 1 {
			t.Errorf("expected 1 violation, got %d: %v", len(violations), violations)
		}
	})

	t.Run("required component missing", func(t *testing.T) {
		rel := model.BoriRelease{
			Components: []model.ComponentRef{
				{Name: "jumi", Version: "v0.3.0"},
			},
		}
		violations := CheckCompatibility(rel, matrix)
		if len(violations) != 1 {
			t.Errorf("expected 1 violation, got %d: %v", len(violations), violations)
		}
	})
}

func TestOrder(t *testing.T) {
	rel := model.BoriRelease{
		Components: []model.ComponentRef{
			{Name: "jumi", Version: "v0.3.0"},
			{Name: "artifact-handoff", Version: "v0.2.0"},
			{Name: "nan", Version: "v0.1.5"},
		},
	}
	comps := map[string]model.BoriComponent{
		"jumi": {
			Name:         "jumi",
			Dependencies: []string{"artifact-handoff"},
		},
		"artifact-handoff": {
			Name:         "artifact-handoff",
			Dependencies: []string{},
		},
		"nan": {
			Name:         "nan",
			Dependencies: []string{"artifact-handoff"},
		},
	}

	ordered, err := Order(rel, comps)
	if err != nil {
		t.Fatalf("Order error: %v", err)
	}
	if len(ordered) != 3 {
		t.Fatalf("expected 3 components, got %d", len(ordered))
	}
	// artifact-handoff must come before jumi and nan
	pos := make(map[string]int, 3)
	for i, ref := range ordered {
		pos[ref.Name] = i
	}
	if pos["artifact-handoff"] >= pos["jumi"] {
		t.Errorf("artifact-handoff (%d) should come before jumi (%d)", pos["artifact-handoff"], pos["jumi"])
	}
	if pos["artifact-handoff"] >= pos["nan"] {
		t.Errorf("artifact-handoff (%d) should come before nan (%d)", pos["artifact-handoff"], pos["nan"])
	}
}

func TestAffectedComponents(t *testing.T) {
	refs := []model.ComponentRef{
		{Name: "artifact-handoff", Version: "v0.2.0"},
		{Name: "jumi", Version: "v0.3.0"},
		{Name: "nan", Version: "v0.1.5"},
	}
	comps := map[string]model.BoriComponent{
		"artifact-handoff": {Dependencies: []string{}},
		"jumi":             {Dependencies: []string{"artifact-handoff"}},
		"nan":              {Dependencies: []string{"artifact-handoff"}},
	}

	t.Run("direct change", func(t *testing.T) {
		affected := AffectedComponents([]string{"jumi"}, refs, comps)
		if len(affected) != 1 || affected[0] != "jumi" {
			t.Errorf("expected [jumi], got %v", affected)
		}
	})

	t.Run("transitive change", func(t *testing.T) {
		affected := AffectedComponents([]string{"artifact-handoff"}, refs, comps)
		// artifact-handoff change should affect jumi and nan too
		if len(affected) != 3 {
			t.Errorf("expected 3 affected, got %v", affected)
		}
	})
}
