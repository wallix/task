package taskfile

import (
	"github.com/wallix/task/v3/internal/fsext"
)

type Node interface {
	Read() ([]byte, error)
	Parent() Node
	Location() string
	Dir() string
	ResolveEntrypoint(entrypoint string) (string, error)
	ResolveDir(dir string) (string, error)
}

func NewRootNode(
	entrypoint string,
	dir string,
) (Node, error) {
	dir = fsext.DefaultDir(entrypoint, dir)
	// If the entrypoint is "-", we read from stdin
	if entrypoint == "-" {
		return NewStdinNode(dir)
	}
	return NewNode(entrypoint, dir)
}

func NewNode(
	entrypoint string,
	dir string,
	opts ...NodeOption,
) (Node, error) {
	return NewFileNode(entrypoint, dir, opts...)
}
