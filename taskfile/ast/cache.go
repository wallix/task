package ast

import (
	"go.yaml.in/yaml/v3"

	"github.com/go-task/task/v3/errors"
)

// Cache configures per-task remote cache and distributed locking.
//
//   cache:
//     enabled: test -n "$REDIS_URL"      # shell command (exit 0 = on) or bool
//     url: echo "file:///cache/$TASK_CACHE_HASH.zip"  # shell → cache URL
//     lock: echo "redis://host/lock:name"             # shell → lock URL
type Cache struct {
	Enabled *bool  // explicit bool (nil = always enabled when block present)
	If      string // shell command for dynamic enable check
	URL     string // shell command that outputs a cache URL
	Lock    string // shell command that outputs a lock URL
}

func (c *Cache) DeepCopy() *Cache {
	if c == nil {
		return nil
	}
	cp := *c
	if c.Enabled != nil {
		v := *c.Enabled
		cp.Enabled = &v
	}
	return &cp
}

func (c *Cache) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return errors.NewTaskfileDecodeError(nil, node).WithTypeMessage("cache")
	}

	// Iterate key-value pairs to handle enabled's dual type (bool or string).
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		val := node.Content[i+1]
		switch key.Value {
		case "enabled":
			if val.Tag == "!!bool" {
				var b bool
				if err := val.Decode(&b); err != nil {
					return errors.NewTaskfileDecodeError(err, node)
				}
				c.Enabled = &b
			} else {
				c.If = val.Value
			}
		case "url":
			c.URL = val.Value
		case "lock":
			c.Lock = val.Value
		}
	}
	return nil
}
