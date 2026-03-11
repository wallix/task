package taskfile

import (
	"context"
	"os"

	"github.com/dominikbraun/graph"
	"go.yaml.in/yaml/v3"
	"golang.org/x/sync/errgroup"

	"github.com/wallix/task/v3/errors"
	"github.com/wallix/task/v3/internal/env"
	"github.com/wallix/task/v3/internal/filepathext"
	"github.com/wallix/task/v3/internal/templater"
	"github.com/wallix/task/v3/taskfile/ast"
)

type (
	// DebugFunc is a function that can be called to log debug messages.
	DebugFunc func(string)
	// PromptFunc is a function that can be called to prompt the user for input.
	PromptFunc func(string) error
	// A ReaderOption is any type that can apply a configuration to a [Reader].
	ReaderOption interface {
		ApplyToReader(*Reader)
	}
	// A Reader will recursively read Taskfiles from a given [Node] and build a
	// [ast.TaskfileGraph] from them.
	Reader struct {
		graph      *ast.TaskfileGraph
		tempDir    string
		debugFunc  DebugFunc
		promptFunc PromptFunc
	}
)

// NewReader constructs a new Taskfile [Reader] using the given Node and
// options.
func NewReader(opts ...ReaderOption) *Reader {
	r := &Reader{
		graph:      ast.NewTaskfileGraph(),
		tempDir:    os.TempDir(),
		debugFunc:  nil,
		promptFunc: nil,
	}
	r.Options(opts...)
	return r
}

// Options loops through the given [ReaderOption] functions and applies them to
// the [Reader].
func (r *Reader) Options(opts ...ReaderOption) {
	for _, opt := range opts {
		opt.ApplyToReader(r)
	}
}

// WithTempDir sets the temporary directory that will be used by the [Reader].
// By default, the reader uses [os.TempDir].
func WithTempDir(tempDir string) ReaderOption {
	return &tempDirOption{tempDir: tempDir}
}

type tempDirOption struct {
	tempDir string
}

func (o *tempDirOption) ApplyToReader(r *Reader) {
	r.tempDir = o.tempDir
}

// WithDebugFunc sets the debug function to be used by the [Reader]. If set,
// this function will be called with debug messages. This can be useful if the
// caller wants to log debug messages from the [Reader]. By default, no debug
// function is set and the logs are not written.
func WithDebugFunc(debugFunc DebugFunc) ReaderOption {
	return &debugFuncOption{debugFunc: debugFunc}
}

type debugFuncOption struct {
	debugFunc DebugFunc
}

func (o *debugFuncOption) ApplyToReader(r *Reader) {
	r.debugFunc = o.debugFunc
}

// WithPromptFunc sets the prompt function to be used by the [Reader]. If set,
// this function will be called with prompt messages. The function should
// optionally log the message to the user and return nil if the prompt is
// accepted and the execution should continue. Otherwise, it should return an
// error which describes why the prompt was rejected. This can then be caught
// and used later when calling the [Reader.Read] method. By default, no prompt
// function is set and all prompts are automatically accepted.
func WithPromptFunc(promptFunc PromptFunc) ReaderOption {
	return &promptFuncOption{promptFunc: promptFunc}
}

type promptFuncOption struct {
	promptFunc PromptFunc
}

func (o *promptFuncOption) ApplyToReader(r *Reader) {
	r.promptFunc = o.promptFunc
}

// Read will read the Taskfile defined by the [Reader]'s [Node] and recurse
// through any [ast.Includes] it finds, reading each included Taskfile and
// building an [ast.TaskfileGraph] as it goes. If any errors occur, they will be
// returned immediately.
func (r *Reader) Read(ctx context.Context, node Node) (*ast.TaskfileGraph, error) {
	if err := r.include(ctx, node); err != nil {
		return nil, err
	}

	return r.graph, nil
}

func (r *Reader) include(ctx context.Context, node Node) error {
	// Create a new vertex for the Taskfile
	vertex := &ast.TaskfileVertex{
		URI:      node.Location(),
		Taskfile: nil,
	}

	// Add the included Taskfile to the DAG
	// If the vertex already exists, we return early since its Taskfile has
	// already been read and its children explored
	if err := r.graph.AddVertex(vertex); err == graph.ErrVertexAlreadyExists {
		return nil
	} else if err != nil {
		return err
	}

	// Read and parse the Taskfile from the file and add it to the vertex
	var err error
	vertex.Taskfile, err = r.readNode(ctx, node)
	if err != nil {
		return err
	}

	// Create an error group to wait for all included Taskfiles to be read
	var g errgroup.Group

	// Loop over each included taskfile
	for _, include := range vertex.Taskfile.Includes.All() {
		vars := env.GetEnviron()
		vars.Merge(vertex.Taskfile.Vars, nil)
		// Start a goroutine to process each included Taskfile
		g.Go(func() error {
			cache := &templater.Cache{Vars: vars}
			include = &ast.Include{
				Namespace:      include.Namespace,
				Taskfile:       templater.Replace(include.Taskfile, cache),
				Dir:            templater.Replace(include.Dir, cache),
				Optional:       include.Optional,
				Internal:       include.Internal,
				Flatten:        include.Flatten,
				Aliases:        include.Aliases,
				AdvancedImport: include.AdvancedImport,
				Excludes:       include.Excludes,
				Vars:           include.Vars,
			}
			if err := cache.Err(); err != nil {
				return err
			}

			entrypoint, err := node.ResolveEntrypoint(include.Taskfile)
			if err != nil {
				return err
			}

			include.Dir, err = node.ResolveDir(include.Dir)
			if err != nil {
				return err
			}

			includeNode, err := NewNode(entrypoint, include.Dir,
				WithParent(node),
			)
			if err != nil {
				if include.Optional {
					return nil
				}
				return err
			}

			// Recurse into the included Taskfile
			if err := r.include(ctx, includeNode); err != nil {
				return err
			}

			// Create an edge between the Taskfiles
			r.graph.Lock()
			defer r.graph.Unlock()
			edge, err := r.graph.Edge(node.Location(), includeNode.Location())
			if err == graph.ErrEdgeNotFound {
				// If the edge doesn't exist, create it
				err = r.graph.AddEdge(
					node.Location(),
					includeNode.Location(),
					graph.EdgeData([]*ast.Include{include}),
					graph.EdgeWeight(1),
				)
			} else {
				// If the edge already exists
				edgeData := append(edge.Properties.Data.([]*ast.Include), include)
				err = r.graph.UpdateEdge(
					node.Location(),
					includeNode.Location(),
					graph.EdgeData(edgeData),
					graph.EdgeWeight(len(edgeData)),
				)
			}
			if errors.Is(err, graph.ErrEdgeCreatesCycle) {
				return errors.TaskfileCycleError{
					Source:      node.Location(),
					Destination: includeNode.Location(),
				}
			}
			return err
		})
	}

	// Wait for all the go routines to finish
	return g.Wait()
}

func (r *Reader) readNode(ctx context.Context, node Node) (*ast.Taskfile, error) {
	b, err := node.Read()
	if err != nil {
		return nil, err
	}

	var tf ast.Taskfile
	if err := yaml.Unmarshal(b, &tf); err != nil {
		// Decode the taskfile and add the file info the any errors
		taskfileDecodeErr := &errors.TaskfileDecodeError{}
		if errors.As(err, &taskfileDecodeErr) {
			snippet := NewSnippet(b,
				WithLine(taskfileDecodeErr.Line),
				WithColumn(taskfileDecodeErr.Column),
				WithPadding(2),
			)
			return nil, taskfileDecodeErr.WithFileInfo(node.Location(), snippet.String())
		}
		return nil, &errors.TaskfileInvalidError{URI: filepathext.TryAbsToRel(node.Location()), Err: err}
	}

	// Check that the Taskfile is set and has a schema version
	if tf.Version == nil {
		return nil, &errors.TaskfileVersionCheckError{URI: node.Location()}
	}

	// Set the taskfile/task's locations
	tf.Location = node.Location()
	for task := range tf.Tasks.Values(nil) {
		// If the task is not defined, create a new one
		if task == nil {
			task = &ast.Task{}
		}
		// Set the location of the taskfile for each task
		if task.Location.Taskfile == "" {
			task.Location.Taskfile = tf.Location
		}
	}

	return &tf, nil
}
