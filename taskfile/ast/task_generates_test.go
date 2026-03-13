package ast_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/wallix/task/v3/taskfile/ast"
)

func TestTask_Generates_Fingerprint(t *testing.T) {
	t.Parallel()
	input := `
cmds:
  - yarn install --immutable
sources:
  - package.json
  - yarn.lock
generates:
  - glob: "node_modules/**/*"
    fingerprint: "node_modules/.yarn-state.yml"
`
	var task ast.Task
	require.NoError(t, yaml.Unmarshal([]byte(input), &task))

	require.Len(t, task.Generates, 1)
	assert.Equal(t, "node_modules/**/*", task.Generates[0].Glob)
	assert.Equal(t, "node_modules/.yarn-state.yml", task.Generates[0].Fingerprint)
	assert.False(t, task.Generates[0].Negate)

	require.Len(t, task.Sources, 2)
	assert.Equal(t, "package.json", task.Sources[0].Glob)
	assert.Equal(t, "yarn.lock", task.Sources[1].Glob)
}

func TestTask_Generates_Mixed(t *testing.T) {
	t.Parallel()
	input := `
cmds:
  - make build
generates:
  - "build/**/*"
  - glob: "node_modules/**/*"
    fingerprint: "node_modules/.yarn-state.yml"
  - exclude: "build/tmp/**"
`
	var task ast.Task
	require.NoError(t, yaml.Unmarshal([]byte(input), &task))

	require.Len(t, task.Generates, 3)

	// Plain glob
	assert.Equal(t, "build/**/*", task.Generates[0].Glob)
	assert.Empty(t, task.Generates[0].Fingerprint)
	assert.False(t, task.Generates[0].Negate)

	// Glob with fingerprint
	assert.Equal(t, "node_modules/**/*", task.Generates[1].Glob)
	assert.Equal(t, "node_modules/.yarn-state.yml", task.Generates[1].Fingerprint)
	assert.False(t, task.Generates[1].Negate)

	// Exclude
	assert.Equal(t, "build/tmp/**", task.Generates[2].Glob)
	assert.Empty(t, task.Generates[2].Fingerprint)
	assert.True(t, task.Generates[2].Negate)
}
