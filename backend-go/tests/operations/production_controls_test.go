package operations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionControlContract(t *testing.T) {
	root := repositoryRoot(t)
	configuration := readOperationsDocument(t, root, "configuration.md")
	topology := readOperationsDocument(t, root, "topology.md")
	guidance := strings.ToLower(strings.Join(strings.Fields(configuration+"\n"+topology), " "))

	for _, requirement := range [][]string{
		{"managed secret store"},
		{"tls-terminating ingress"},
		{"go is the only publicly exposed", "application service"},
		{"rust has no public listener", "host port, route, or ingress"},
		{"provider selection is deferred"},
		{"docker compose"},
		{"healthz"},
		{"docker run --rm --network"},
		{"rollback"},
	} {
		for _, required := range requirement {
			if !strings.Contains(guidance, required) {
				t.Errorf("operations guidance is missing required production control or command %q", required)
			}
		}
	}

	production := resolveComposeModel(t, root, "production")
	assertTopologyInvariants(t, "production", production)
}

func readOperationsDocument(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, "docs", "operations", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(fmt.Errorf("read %s: %w", path, err))
	}
	return string(contents)
}
