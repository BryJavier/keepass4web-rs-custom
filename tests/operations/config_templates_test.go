package operations

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type templateSpec struct {
	path         string
	requiredKeys []string
}

func TestConfigurationTemplates(t *testing.T) {
	root := repositoryRoot(t)
	specs := []templateSpec{
		{
			path: "deploy/config/go.env.example",
			requiredKeys: []string{
				"APP_ENV", "APP_LISTEN_ADDR", "APP_SESSION_SECRET", "SUPABASE_URL",
				"SUPABASE_ANON_KEY", "SUPABASE_JWT_ISSUER", "RUST_SERVICE_URL", "RUST_SERVICE_TOKEN",
			},
		},
		{
			path:         "deploy/config/rust.env.example",
			requiredKeys: []string{"RUST_LISTEN_ADDR", "RUST_SERVICE_TOKEN"},
		},
		{
			path:         "deploy/config/supabase.env.example",
			requiredKeys: []string{"SUPABASE_URL", "SUPABASE_ANON_KEY", "SUPABASE_SERVICE_ROLE_KEY"},
		},
	}

	for _, spec := range specs {
		t.Run(spec.path, func(t *testing.T) {
			values, err := parseEnvironmentExample(filepath.Join(root, spec.path))
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range spec.requiredKeys {
				if _, ok := values[key]; !ok {
					t.Errorf("missing required key %q", key)
				}
			}
			if len(values) != len(spec.requiredKeys) {
				t.Errorf("template defines %d keys, want exactly %d service-specific keys", len(values), len(spec.requiredKeys))
			}
		})
	}

	for _, service := range []string{"go", "rust", "supabase"} {
		for _, environment := range []string{"development", "staging", "production"} {
			target := filepath.Join("deploy", "config", service+"."+environment+".env")
			command := exec.Command("git", "check-ignore", "--no-index", "--quiet", target)
			command.Dir = root
			if err := command.Run(); err != nil {
				t.Errorf("%s must be ignored: %v", target, err)
			}
		}
	}

	for _, service := range []string{"go", "rust", "supabase"} {
		example := filepath.Join("deploy", "config", service+".env.example")
		command := exec.Command("git", "check-ignore", "--no-index", "--quiet", example)
		command.Dir = root
		if err := command.Run(); err == nil {
			t.Errorf("%s must remain eligible for tracking", example)
		}
	}
}

func TestParseEnvironmentExampleRejectsUnsafeValues(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{
		"blank":  "APP_ENV=\n",
		"secret": "APP_SESSION_SECRET=eyJhbGciOiJIUzI1NiJ9.payload.signature\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name+".env.example")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := parseEnvironmentExample(path); err == nil {
				t.Fatalf("parseEnvironmentExample(%q) error = nil, want unsafe value rejection", name)
			}
		})
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func parseEnvironmentExample(path string) (map[string]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	values := make(map[string]string)
	for lineNumber, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%s:%d must use KEY=value syntax", path, lineNumber+1)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if _, duplicate := values[key]; duplicate {
			return nil, fmt.Errorf("%s:%d duplicates key %q", path, lineNumber+1, key)
		}
		if value == "" {
			return nil, fmt.Errorf("%s:%d leaves %q blank", path, lineNumber+1, key)
		}
		if looksSecretShaped(value) {
			return nil, fmt.Errorf("%s:%d has a secret-shaped value for %q", path, lineNumber+1, key)
		}
		values[key] = value
	}
	return values, nil
}

func looksSecretShaped(value string) bool {
	return strings.HasPrefix(value, "eyJ") ||
		strings.HasPrefix(value, "sk_") ||
		strings.HasPrefix(value, "sb_secret_") ||
		(len(value) > 30 && strings.Count(value, ".") == 2 && !strings.Contains(value, "://"))
}
