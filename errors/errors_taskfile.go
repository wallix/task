package errors

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// TaskfileNotFoundError is returned when no appropriate Taskfile is found when
// searching the filesystem.
type TaskfileNotFoundError struct {
	URI         string
	Walk        bool
	AskInit     bool
	OwnerChange bool
}

func (err TaskfileNotFoundError) Error() string {
	var walkText string
	if err.OwnerChange {
		walkText = " (or any of the parent directories until ownership changed)."
	} else if err.Walk {
		walkText = " (or any of the parent directories)."
	}
	if err.AskInit {
		walkText += " Run `task --init` to create a new Taskfile."
	}
	return fmt.Sprintf(`task: No Taskfile found at %q%s`, err.URI, walkText)
}

func (err TaskfileNotFoundError) Code() int {
	return CodeTaskfileNotFound
}

// TaskfileAlreadyExistsError is returned on creating a Taskfile if one already
// exists.
type TaskfileAlreadyExistsError struct{}

func (err TaskfileAlreadyExistsError) Error() string {
	return "task: A Taskfile already exists"
}

func (err TaskfileAlreadyExistsError) Code() int {
	return CodeTaskfileAlreadyExists
}

// TaskfileInvalidError is returned when the Taskfile contains syntax errors or
// cannot be parsed for some reason.
type TaskfileInvalidError struct {
	URI string
	Err error
}

func (err TaskfileInvalidError) Error() string {
	return fmt.Sprintf("task: Failed to parse %s:\n%v", err.URI, err.Err)
}

func (err TaskfileInvalidError) Code() int {
	return CodeTaskfileInvalid
}

// TaskfileVersionCheckError is returned when the user attempts to run a
// Taskfile that does not contain a Taskfile schema version key or if they try
// to use a feature that is not supported by the schema version.
type TaskfileVersionCheckError struct {
	URI           string
	SchemaVersion *semver.Version
	Message       string
}

func (err *TaskfileVersionCheckError) Error() string {
	if err.SchemaVersion == nil {
		return fmt.Sprintf(
			`task: Missing schema version in Taskfile %q`,
			err.URI,
		)
	}
	return fmt.Sprintf(
		"task: Invalid schema version in Taskfile %q:\nSchema version (%s) %s",
		err.URI,
		err.SchemaVersion.String(),
		err.Message,
	)
}

func (err *TaskfileVersionCheckError) Code() int {
	return CodeTaskfileVersionCheckError
}

// TaskfileCycleError is returned when we detect that a Taskfile includes a
// set of Taskfiles that include each other in a cycle.
type TaskfileCycleError struct {
	Source      string
	Destination string
}

func (err TaskfileCycleError) Error() string {
	return fmt.Sprintf("task: include cycle detected between %s <--> %s",
		err.Source,
		err.Destination,
	)
}

func (err TaskfileCycleError) Code() int {
	return CodeTaskfileCycle
}
