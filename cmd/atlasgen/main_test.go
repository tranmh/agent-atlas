package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestGeneratedFilesAreFresh fails when scenarios changed without `make gen`.
func TestGeneratedFilesAreFresh(t *testing.T) {
	files, err := Generate()
	require.NoError(t, err)
	for path, content := range files {
		var v any
		require.NoError(t, yaml.Unmarshal(content, &v), path)
		disk, err := os.ReadFile(filepath.Join("..", "..", path))
		require.NoError(t, err)
		require.Equal(t, string(content), string(disk), "%s is stale: run make gen", path)
	}
}

// agentgateway expands $VARS everywhere in its config, comments included; an
// unset variable makes the whole config fail to load.
func TestGatewayConfigHasNoEnvReferences(t *testing.T) {
	files, err := Generate()
	require.NoError(t, err)
	cfg := strings.ReplaceAll(string(files[GatewayConfigPath]), "$schema", "")
	require.NotContains(t, cfg, "$")
}
