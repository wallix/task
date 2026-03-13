package ast

import (
	"go.yaml.in/yaml/v3"

	"github.com/wallix/task/v3/errors"
)

// Glob represents a file pattern used in sources and generates lists.
// It supports three YAML forms:
//
//	# scalar: simple glob pattern
//	- "src/**/*.go"
//
//	# exclude: negated pattern
//	- exclude: "vendor/**"
//
//	# glob + fingerprint: the glob defines the full set of files (used for
//	#   caching), while fingerprint names a single representative file used
//	#   for checksum-based up-to-date detection instead of hashing every
//	#   file matched by the glob.
//	- glob: "node_modules/**/*"
//	  fingerprint: "node_modules/.yarn-state.yml"
type Glob struct {
	Glob        string
	Negate      bool
	Fingerprint string // when set, only this file is hashed for up-to-date checks
}

func (g *Glob) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {

	case yaml.ScalarNode:
		g.Glob = node.Value
		return nil

	case yaml.MappingNode:
		var glob struct {
			Exclude     string `yaml:"exclude"`
			Glob        string `yaml:"glob"`
			Fingerprint string `yaml:"fingerprint"`
		}
		if err := node.Decode(&glob); err != nil {
			return errors.NewTaskfileDecodeError(err, node)
		}
		if glob.Exclude != "" {
			g.Glob = glob.Exclude
			g.Negate = true
		} else {
			g.Glob = glob.Glob
		}
		g.Fingerprint = glob.Fingerprint
		return nil
	}

	return errors.NewTaskfileDecodeError(nil, node).WithTypeMessage("glob")
}
