package task

import (
	"encoding/json"
	"fmt"

	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/internal/logger"
)

// Status prints the fingerprint status of the given tasks.
func (e *Executor) Status(calls ...*Call) error {
	return e.status(calls, false)
}

// StatusJSON prints the fingerprint status of the given tasks as JSON.
func (e *Executor) StatusJSON(calls ...*Call) error {
	return e.status(calls, true)
}

func (e *Executor) status(calls []*Call, asJSON bool) error {
	checker := fingerprint.NewChecksumChecker(e.TempDir.Fingerprint)

	var statuses []*fingerprint.TaskStatus
	for _, call := range calls {
		t, err := e.CompiledTask(call)
		if err != nil {
			return err
		}

		if len(t.Sources) == 0 && len(t.Generates) == 0 {
			if asJSON {
				statuses = append(statuses, &fingerprint.TaskStatus{
					Task: t.Name(),
				})
			} else {
				e.Logger.Outf(logger.Yellow, "task: %q has no sources or generates\n", t.Name())
			}
			continue
		}

		st, err := checker.Status(t)
		if err != nil {
			return err
		}
		statuses = append(statuses, st)
	}

	if asJSON {
		enc := json.NewEncoder(e.Logger.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(statuses)
	}

	for i, st := range statuses {
		if i > 0 {
			fmt.Fprintln(e.Logger.Stdout)
		}
		printStatus(e.Logger, st)
	}
	return nil
}

func printStatus(l *logger.Logger, st *fingerprint.TaskStatus) {
	if st.ChecksumFile == "" {
		// Already printed "no sources or generates" above
		return
	}

	if st.UpToDate {
		l.Outf(logger.Green, "task: %q is up to date\n", st.Task)
	} else {
		l.Outf(logger.Red, "task: %q is not up to date\n", st.Task)
	}

	fmt.Fprintf(l.Stdout, "  checksum file: %s\n", st.ChecksumFile)

	if st.SourcesUpToDate {
		fmt.Fprintf(l.Stdout, "  sources: up to date (hash: %s)\n", st.SourcesHash)
	} else {
		fmt.Fprintf(l.Stdout, "  sources: changed (stored hash: %s)\n", st.SourcesHash)
	}
	for _, f := range st.SourceFiles {
		fmt.Fprintf(l.Stdout, "    src: %s\n", f)
	}
	for _, d := range st.SourceData {
		fmt.Fprintf(l.Stdout, "    data: %s\n", d)
	}

	if st.GeneratesUpToDate {
		fmt.Fprintf(l.Stdout, "  generates: up to date (hash: %s)\n", st.GeneratesHash)
	} else {
		fmt.Fprintf(l.Stdout, "  generates: changed (stored hash: %s)\n", st.GeneratesHash)
	}
	for _, f := range st.GenerateFiles {
		fmt.Fprintf(l.Stdout, "    gen: %s\n", f)
	}
}
