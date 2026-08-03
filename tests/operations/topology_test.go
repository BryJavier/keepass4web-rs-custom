package operations

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type resolvedCompose struct {
	Services map[string]resolvedService `json:"services"`
	Networks map[string]resolvedNetwork `json:"networks"`
}

type resolvedService struct {
	Ports    []json.RawMessage `json:"ports"`
	Networks map[string]any    `json:"networks"`
}

type resolvedNetwork struct {
	Internal bool `json:"internal"`
}

func TestResolvedTopology(t *testing.T) {
	root := repositoryRoot(t)

	for _, environment := range []string{"development", "staging", "production"} {
		t.Run(environment, func(t *testing.T) {
			model := resolveComposeModel(t, root, environment)
			assertTopologyInvariants(t, environment, model)
		})
	}
}

func resolveComposeModel(t *testing.T, root, environment string) resolvedCompose {
	t.Helper()
	command := exec.Command(
		"docker", "compose",
		"-f", "docker-compose.yml",
		"-f", filepath.Join("deploy", "compose", environment+".yml"),
		"config", "--format", "json",
	)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WEB_IMAGE=registry.example.test/keepass4web-web@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"RUST_IMAGE=registry.example.test/keepass4web-rust@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve %s Compose configuration: %v\n%s", environment, err, output)
	}

	var model resolvedCompose
	if err := json.Unmarshal(output, &model); err != nil {
		t.Fatalf("decode %s resolved Compose JSON: %v\n%s", environment, err, output)
	}
	return model
}

func assertTopologyInvariants(t *testing.T, environment string, model resolvedCompose) {
	t.Helper()
	web, ok := model.Services["web"]
	if !ok {
		t.Fatalf("%s is missing required web service", environment)
	}
	rust, ok := model.Services["rust-service"]
	if !ok {
		t.Fatalf("%s is missing required rust-service", environment)
	}

	published := make([]string, 0)
	for name, service := range model.Services {
		if len(service.Ports) > 0 {
			published = append(published, name)
		}
	}
	if strings.Join(published, ",") != "web" || len(web.Ports) != 1 {
		t.Fatalf("%s publishes %v; want exactly web with one host port", environment, published)
	}
	if len(rust.Ports) != 0 {
		t.Fatalf("%s rust-service has host ports; Rust must remain private", environment)
	}
	if _, hasPrivateNetwork := rust.Networks["private"]; len(rust.Networks) != 1 || !hasPrivateNetwork {
		t.Fatalf("%s rust-service networks = %v; want private only", environment, networkNames(rust.Networks))
	}
	if model.Networks["private"].Internal != true {
		t.Fatalf("%s private network must be internal", environment)
	}
	if _, hasLocalSupabase := model.Services["supabase"]; hasLocalSupabase {
		t.Fatalf("%s publishes a local Supabase service; local Supabase is development tooling only", environment)
	}
}

func networkNames(networks map[string]any) []string {
	names := make([]string, 0, len(networks))
	for name := range networks {
		names = append(names, name)
	}
	return names
}
