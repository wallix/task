package ast_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/wallix/task/v3/taskfile/ast"
)

func TestCmdParse(t *testing.T) {
	t.Parallel()

	const (
		yamlCmd      = `echo "a string command"`
		yamlDep      = `"task-name"`
		yamlTaskCall = `
task: another-task
vars:
  PARAM1: VALUE1
  PARAM2: VALUE2
`
		yamlDeferredCall = `defer: { task: some_task, vars: { PARAM1: "var" } }`
		yamlDeferredCmd  = `defer: echo 'test'`
	)
	tests := []struct {
		content  string
		v        any
		expected any
	}{
		{
			yamlCmd,
			&ast.Cmd{},
			&ast.Cmd{Cmd: `echo "a string command"`},
		},
		{
			yamlTaskCall,
			&ast.Cmd{},
			&ast.Cmd{
				Task: "another-task",
				Vars: ast.NewVars(
					&ast.VarElement{
						Key: "PARAM1",
						Value: ast.Var{
							Value: "VALUE1",
						},
					},
					&ast.VarElement{
						Key: "PARAM2",
						Value: ast.Var{
							Value: "VALUE2",
						},
					},
				),
			},
		},
		{
			yamlDeferredCmd,
			&ast.Cmd{},
			&ast.Cmd{Cmd: "echo 'test'", Defer: true},
		},
		{
			yamlDeferredCall,
			&ast.Cmd{},
			&ast.Cmd{
				Task: "some_task",
				Vars: ast.NewVars(
					&ast.VarElement{
						Key: "PARAM1",
						Value: ast.Var{
							Value: "var",
						},
					},
				),
				Defer: true,
			},
		},
		{
			yamlDep,
			&ast.Dep{},
			&ast.Dep{Task: "task-name"},
		},
		{
			yamlTaskCall,
			&ast.Dep{},
			&ast.Dep{
				Task: "another-task",
				Vars: ast.NewVars(
					&ast.VarElement{
						Key: "PARAM1",
						Value: ast.Var{
							Value: "VALUE1",
						},
					},
					&ast.VarElement{
						Key: "PARAM2",
						Value: ast.Var{
							Value: "VALUE2",
						},
					},
				),
			},
		},
	}
	for _, test := range tests {
		err := yaml.Unmarshal([]byte(test.content), test.v)
		require.NoError(t, err)
		assert.Equal(t, test.expected, test.v)
	}
}

func TestCacheUnmarshalYAML(t *testing.T) {
	t.Parallel()

	t.Run("bare string sets inherit model name", func(t *testing.T) {
		t.Parallel()
		var c ast.Cache
		require.NoError(t, yaml.Unmarshal([]byte("default"), &c))
		assert.Equal(t, "default", c.Inherit)
		assert.Nil(t, c.Enabled)
	})

	t.Run("mapping with bool enabled", func(t *testing.T) {
		t.Parallel()
		var c ast.Cache
		require.NoError(t, yaml.Unmarshal([]byte("enabled: true\nurl: redis://host/key"), &c))
		require.NotNil(t, c.Enabled)
		assert.True(t, *c.Enabled)
		assert.Equal(t, "redis://host/key", c.URL)
	})

	t.Run("mapping with string enabled becomes If", func(t *testing.T) {
		t.Parallel()
		var c ast.Cache
		require.NoError(t, yaml.Unmarshal([]byte(`enabled: '{{ne .FOO ""}}'`), &c))
		assert.Nil(t, c.Enabled)
		assert.Equal(t, `{{ne .FOO ""}}`, c.If)
	})

	t.Run("mapping with url and lock", func(t *testing.T) {
		t.Parallel()
		var c ast.Cache
		require.NoError(t, yaml.Unmarshal([]byte("url: file:///tmp/x.zip\nlock: redis://host/lock"), &c))
		assert.Equal(t, "file:///tmp/x.zip", c.URL)
		assert.Equal(t, "redis://host/lock", c.Lock)
	})

	t.Run("taskfile-level caches map", func(t *testing.T) {
		t.Parallel()
		var tf ast.Taskfile
		require.NoError(t, yaml.Unmarshal([]byte(`
version: '3'
caches:
  default:
    url: 'redis://host/{{.CHECKSUM}}'
  doc:
    enabled: false
    url: 'file:///tmp/doc.zip'
tasks:
  build:
    cmds:
      - echo hi
`), &tf))
		require.Len(t, tf.Caches, 2)
		assert.Equal(t, "redis://host/{{.CHECKSUM}}", tf.Caches["default"].URL)
		require.NotNil(t, tf.Caches["doc"].Enabled)
		assert.False(t, *tf.Caches["doc"].Enabled)
	})

	t.Run("mapping with inherit name and url override", func(t *testing.T) {
		t.Parallel()
		var c ast.Cache
		require.NoError(t, yaml.Unmarshal([]byte("inherit: doc\nurl: file:///override"), &c))
		assert.Equal(t, "doc", c.Inherit)
		assert.Equal(t, "file:///override", c.URL)
	})

	t.Run("mapping with ttl", func(t *testing.T) {
		t.Parallel()
		var c ast.Cache
		require.NoError(t, yaml.Unmarshal([]byte("url: redis://host/key\nttl: 48h"), &c))
		assert.Equal(t, "redis://host/key", c.URL)
		assert.Equal(t, "48h", c.TTL)
	})

	t.Run("ttl inherited from caches map via model name", func(t *testing.T) {
		t.Parallel()
		var tf ast.Taskfile
		require.NoError(t, yaml.Unmarshal([]byte(`
version: '3'
caches:
  default:
    url: 'redis://host/{{.CHECKSUM}}'
    ttl: 72h
tasks:
  build:
    cmds:
      - echo hi
`), &tf))
		require.Contains(t, tf.Caches, "default")
		assert.Equal(t, "72h", tf.Caches["default"].TTL)
	})

	t.Run("ttl in mapping with other fields", func(t *testing.T) {
		t.Parallel()
		var c ast.Cache
		require.NoError(t, yaml.Unmarshal([]byte("inherit: doc\nurl: file:///tmp/x.zip\nlock: redis://host/lock\nttl: 7d"), &c))
		assert.Equal(t, "doc", c.Inherit)
		assert.Equal(t, "file:///tmp/x.zip", c.URL)
		assert.Equal(t, "redis://host/lock", c.Lock)
		assert.Equal(t, "7d", c.TTL)
	})

	t.Run("ttl absent defaults to empty string", func(t *testing.T) {
		t.Parallel()
		var c ast.Cache
		require.NoError(t, yaml.Unmarshal([]byte("url: redis://host/key"), &c))
		assert.Equal(t, "", c.TTL)
	})

	t.Run("bare string model has no ttl", func(t *testing.T) {
		t.Parallel()
		var c ast.Cache
		require.NoError(t, yaml.Unmarshal([]byte("default"), &c))
		assert.Equal(t, "default", c.Inherit)
		assert.Equal(t, "", c.TTL)
	})
}
