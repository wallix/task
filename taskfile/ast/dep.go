package ast

import (
	"go.yaml.in/yaml/v3"

	"github.com/go-task/task/v3/errors"
	"github.com/go-task/task/v3/internal/deepcopy"
)

// Dep is a task dependency
type Dep struct {
	Task        string
	For         *For
	Vars        *Vars
	Silent      bool
	Fingerprint *bool
}

func (d *Dep) DeepCopy() *Dep {
	if d == nil {
		return nil
	}
	return &Dep{
		Task:        d.Task,
		For:         d.For.DeepCopy(),
		Vars:        d.Vars.DeepCopy(),
		Silent:      d.Silent,
		Fingerprint: deepcopy.Scalar(d.Fingerprint),
	}
}

func (d *Dep) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {

	case yaml.ScalarNode:
		var task string
		if err := node.Decode(&task); err != nil {
			return errors.NewTaskfileDecodeError(err, node)
		}
		d.Task = task
		return nil

	case yaml.MappingNode:
		var taskCall struct {
			Task        string
			For         *For
			Vars        *Vars
			Silent      bool
			Fingerprint *bool `yaml:"fingerprint,omitempty"`
		}
		if err := node.Decode(&taskCall); err != nil {
			return errors.NewTaskfileDecodeError(err, node)
		}
		d.Task = taskCall.Task
		d.For = taskCall.For
		d.Vars = taskCall.Vars
		d.Silent = taskCall.Silent
		d.Fingerprint = deepcopy.Scalar(taskCall.Fingerprint)
		return nil
	}

	return errors.NewTaskfileDecodeError(nil, node).WithTypeMessage("dependency")
}
