package task

import (
	"fmt"

	"github.com/go-task/task/v3/internal/fingerprint"
	"github.com/go-task/task/v3/internal/logger"
)

// Status prints the fingerprint status of the given tasks.
func (e *Executor) Status(calls ...*Call) error {
	checker := fingerprint.NewChecksumChecker(e.TempDir.Fingerprint)

	for i, call := range calls {
		t, err := e.CompiledTask(call)
		if err != nil {
			return err
		}

		if i > 0 {
			fmt.Fprintln(e.Logger.Stdout)
		}

		if len(t.Sources) == 0 && len(t.Generates) == 0 {
			e.Logger.Outf(logger.Yellow, "task: %q has no sources or generates\n", t.Name())
			continue
		}

		upToDate, _, err := checker.IsUpToDate(t)
		if err != nil {
			return err
		}

		if upToDate {
			e.Logger.Outf(logger.Green, "task: %q is up to date\n", t.Name())
		} else {
			e.Logger.Outf(logger.Red, "task: %q is not up to date\n", t.Name())
		}

		checksumFile, srcHash, srcFiles, srcData, genHash, genFiles := checker.State(t)

		fmt.Fprintf(e.Logger.Stdout, "  checksum file: %s\n", checksumFile)
		fmt.Fprintf(e.Logger.Stdout, "  sources hash:  %s\n", srcHash)
		for _, f := range srcFiles {
			fmt.Fprintf(e.Logger.Stdout, "    src: %s\n", f)
		}
		for _, d := range srcData {
			fmt.Fprintf(e.Logger.Stdout, "    data: %s\n", d)
		}
		fmt.Fprintf(e.Logger.Stdout, "  generates hash: %s\n", genHash)
		for _, f := range genFiles {
			fmt.Fprintf(e.Logger.Stdout, "    gen: %s\n", f)
		}
	}
	return nil
}
