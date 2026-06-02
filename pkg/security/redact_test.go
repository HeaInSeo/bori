package security

import "testing"

func TestRedactString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"no secrets here", "no secrets here"},
		{"Authorization: Bearer abc123xyz", "Authorization: Bearer [REDACTED]"},
		{"Authorization: basic dXNlcjpwYXNz", "Authorization: basic [REDACTED]"},
		{"token abc123", "token [REDACTED]"},
	}
	for _, c := range cases {
		got := RedactString(c.in)
		if got != c.want {
			t.Errorf("RedactString(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestRedactMap(t *testing.T) {
	in := map[string]string{
		"DATABASE_URL":    "postgres://host/db",
		"API_TOKEN":       "secret-value",
		"PASSWORD":        "hunter2",
		"REGISTRY_SECRET": "cred",
		"APP_NAME":        "jumi",
		"METRICS_PORT":    "8080",
	}
	got := RedactMap(in)

	safe := []string{"DATABASE_URL", "APP_NAME", "METRICS_PORT"}
	for _, k := range safe {
		if got[k] != in[k] {
			t.Errorf("RedactMap: key %q should not be redacted, got %q", k, got[k])
		}
	}
	sensitive := []string{"API_TOKEN", "PASSWORD", "REGISTRY_SECRET"}
	for _, k := range sensitive {
		if got[k] != "[REDACTED]" {
			t.Errorf("RedactMap: key %q should be redacted, got %q", k, got[k])
		}
	}
}

func TestRedactEnv(t *testing.T) {
	in := []string{
		"APP_NAME=jumi",
		"REGISTRY_PASSWORD=secret",
		"PORT=8080",
		"AUTH_TOKEN=tok123",
	}
	got := RedactEnv(in)

	if got[0] != "APP_NAME=jumi" {
		t.Errorf("expected APP_NAME unchanged, got %q", got[0])
	}
	if got[1] != "REGISTRY_PASSWORD=[REDACTED]" {
		t.Errorf("expected REGISTRY_PASSWORD redacted, got %q", got[1])
	}
	if got[2] != "PORT=8080" {
		t.Errorf("expected PORT unchanged, got %q", got[2])
	}
	if got[3] != "AUTH_TOKEN=[REDACTED]" {
		t.Errorf("expected AUTH_TOKEN redacted, got %q", got[3])
	}
}
