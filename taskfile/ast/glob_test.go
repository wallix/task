package ast_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/wallix/task/v3/taskfile/ast"
)

func TestGlob_UnmarshalYAML_Scalar(t *testing.T) {
	t.Parallel()
	input := `"src/**/*.go"`
	var g ast.Glob
	require.NoError(t, yaml.Unmarshal([]byte(input), &g))
	assert.Equal(t, "src/**/*.go", g.Glob)
	assert.False(t, g.Negate)
	assert.Empty(t, g.Fingerprint)
}

func TestGlob_UnmarshalYAML_Exclude(t *testing.T) {
	t.Parallel()
	input := `exclude: "vendor/**"`
	var g ast.Glob
	require.NoError(t, yaml.Unmarshal([]byte(input), &g))
	assert.Equal(t, "vendor/**", g.Glob)
	assert.True(t, g.Negate)
	assert.Empty(t, g.Fingerprint)
}

func TestGlob_UnmarshalYAML_GlobWithFingerprint(t *testing.T) {
	t.Parallel()
	input := `
glob: "node_modules/**/*"
fingerprint: "node_modules/.yarn-state.yml"
`
	var g ast.Glob
	require.NoError(t, yaml.Unmarshal([]byte(input), &g))
	assert.Equal(t, "node_modules/**/*", g.Glob)
	assert.False(t, g.Negate)
	assert.Equal(t, "node_modules/.yarn-state.yml", g.Fingerprint)
}

func TestGlob_UnmarshalYAML_GlobWithoutFingerprint(t *testing.T) {
	t.Parallel()
	input := `glob: "build/**/*"`
	var g ast.Glob
	require.NoError(t, yaml.Unmarshal([]byte(input), &g))
	assert.Equal(t, "build/**/*", g.Glob)
	assert.False(t, g.Negate)
	assert.Empty(t, g.Fingerprint)
}

func TestGlob_UnmarshalYAML_InGeneratesList(t *testing.T) {
	t.Parallel()
	input := `
- "build/**/*"
- exclude: "build/tmp/**"
- glob: "node_modules/**/*"
  fingerprint: "node_modules/.yarn-state.yml"
`
	var globs []*ast.Glob
	require.NoError(t, yaml.Unmarshal([]byte(input), &globs))
	require.Len(t, globs, 3)

	assert.Equal(t, "build/**/*", globs[0].Glob)
	assert.False(t, globs[0].Negate)
	assert.Empty(t, globs[0].Fingerprint)

	assert.Equal(t, "build/tmp/**", globs[1].Glob)
	assert.True(t, globs[1].Negate)
	assert.Empty(t, globs[1].Fingerprint)

	assert.Equal(t, "node_modules/**/*", globs[2].Glob)
	assert.False(t, globs[2].Negate)
	assert.Equal(t, "node_modules/.yarn-state.yml", globs[2].Fingerprint)
}
