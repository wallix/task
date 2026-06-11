package ast

import (
	"go.yaml.in/yaml/v3"

	"github.com/wallix/task/v3/errors"
)

// Cache configures per-task remote cache and distributed locking.
//
// At the taskfile level, named cache models are defined in a caches: map:
//
//	caches:
//	  default:
//	    enabled: '{{ne ._CACHE_BASE_URL ""}}'
//	    url: '{{._CACHE_BASE_URL}}cache:{{urlsafe .TASK}}-{{.CHECKSUM}}.zip'
//	    lock: '{{._CACHE_BASE_URL}}lock:{{urlsafe .TASK}}-{{.CHECKSUM}}'
//	    ttl: 48h
//	  doc:
//	    enabled: '{{ne ._CACHE_DOC_BASE_URL ""}}'
//	    url: '{{._CACHE_DOC_BASE_URL}}cache:{{urlsafe .TASK}}-{{.CHECKSUM}}.zip'
//	    lock: '{{._CACHE_DOC_BASE_URL}}lock:{{urlsafe .TASK}}-{{.CHECKSUM}}'
//	  oci:
//	    # chunk-deduplicated OCI artifacts (task_cache_oci.go); no ttl —
//	    # the registry's retention policy prunes old entries
//	    url: 'oci://{{._CACHE_OCI_REPO}}:{{urlsafe .TASK}}-{{.CHECKSUM}}?ca={{._CACHE_OCI_CA}}'
//	    lock: '{{._CACHE_LOCK_URL}}lock:{{urlsafe .TASK}}-{{.CHECKSUM}}'
//
// At the task level, cache: references a model by name or provides overrides:
//
//	cache: default                    # inherit fully from named model
//	cache:
//	  inherit: doc                    # inherit from "doc" model with overrides
//	  url: 'override...'
type Cache struct {
	Inherit     string // model name to inherit from (empty = no inheritance)
	Enabled     *bool  // explicit bool (nil = always enabled when block present)
	If          string // template condition for dynamic enable check
	URL         string // template string → cache URL
	Lock        string // template string → lock URL
	TTL         string // cached asset TTL (e.g. "48h", "7d"); default 48h
	LockTimeout string // max wait for lock contention (e.g. "5m", "1h"); default 1h
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
	switch node.Kind {
	case yaml.ScalarNode:
		// cache: <model-name> (string)
		if node.Tag == "!!str" || node.Tag == "" {
			c.Inherit = node.Value
			return nil
		}
		return errors.NewTaskfileDecodeError(nil, node).WithTypeMessage("cache")

	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			val := node.Content[i+1]
			switch key.Value {
			case "inherit":
				c.Inherit = val.Value
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
			case "ttl":
				c.TTL = val.Value
			case "lock_timeout":
				c.LockTimeout = val.Value
			}
		}
		return nil
	}

	return errors.NewTaskfileDecodeError(nil, node).WithTypeMessage("cache")
}

// Caches is a map of named Cache models defined at the taskfile level.
type Caches map[string]*Cache

func (c *Caches) DeepCopy() Caches {
	if c == nil || *c == nil {
		return nil
	}
	cp := make(Caches, len(*c))
	for k, v := range *c {
		cp[k] = v.DeepCopy()
	}
	return cp
}
