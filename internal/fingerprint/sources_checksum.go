package fingerprint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-task/task/v3/internal/filepathext"
	"github.com/go-task/task/v3/taskfile/ast"
)

// ChecksumChecker validates if a task is up to date by calculating its source
// files checksum
type ChecksumChecker struct {
	tempDir string
	dry     bool
}

func NewChecksumChecker(tempDir string, dry bool) *ChecksumChecker {
	return &ChecksumChecker{
		tempDir: tempDir,
		dry:     dry,
	}
}

func serializeCmd(idx int, c *ast.Cmd) string {
	return fmt.Sprintf("cmd[%d]:%s", idx, c.Cmd)
}

func (checker *ChecksumChecker) filterChecksumData(t *ast.Task) ([]*ast.Glob, []string) {
	var sources []*ast.Glob
	var data []string
	for _, source := range t.Sources {
		if strings.HasPrefix(source.Glob, "value:") {
			data = append(data, source.Glob)
		} else {
			sources = append(sources, source)
			s := source.Glob
			if source.Negate {
				s = "!" + s
			}
			data = append(data, "srcrule:"+s)
		}
	}
	for i, cmd := range t.Cmds {
		data = append(data, serializeCmd(i, cmd))
	}
	for _, genRule := range t.Generates {
		s := genRule.Glob
		if genRule.Negate {
			s = "!" + s
		}
		data = append(data, "genrule:"+s)
	}
	sort.Strings(data)
	return sources, data
}

func (checker *ChecksumChecker) IsUpToDate(t *ast.Task) (bool, error) {
	if len(t.Sources) == 0 {
		return false, nil
	}

	checksumFile := checker.checksumFilePath(t)

	data, _ := os.ReadFile(checksumFile)
	oldHash := strings.TrimSpace(string(data))

	sourcesGlobs, srcData := checker.filterChecksumData(t)
	newHash, err := checker.checksum(t, sourcesGlobs, srcData)
	if err != nil {
		return false, nil
	}

	if !checker.dry && oldHash != newHash {
		_ = os.MkdirAll(filepathext.SmartJoin(checker.tempDir, "checksum"), 0o755)
		if err = os.WriteFile(checksumFile, []byte(newHash+"\n"), 0o644); err != nil {
			return false, err
		}
	}

	if len(t.Generates) > 0 {
		// For each specified 'generates' field, check whether the files actually exist
		for _, g := range t.Generates {
			if g.Negate {
				continue
			}
			generates, err := glob(t.Dir, g.Glob)
			if os.IsNotExist(err) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			if len(generates) == 0 {
				return false, nil
			}
		}
	}

	return oldHash == newHash, nil
}

func (checker *ChecksumChecker) Value(t *ast.Task) (any, error) {
	sourcesGlobs, srcData := checker.filterChecksumData(t)
	return checker.checksum(t, sourcesGlobs, srcData)
}

func (checker *ChecksumChecker) OnError(t *ast.Task) error {
	if len(t.Sources) == 0 {
		return nil
	}
	return os.Remove(checker.checksumFilePath(t))
}

func (*ChecksumChecker) Kind() string {
	return "checksum"
}

func (c *ChecksumChecker) checksum(t *ast.Task, globs []*ast.Glob, data []string) (string, error) {
	sources, err := Globs(t.Dir, globs)
	if err != nil {
		return "", err
	}
	return ChecksumFiles(t.Dir, sources, data)
}

func (checker *ChecksumChecker) checksumFilePath(t *ast.Task) string {
	return filepath.Join(checker.tempDir, "checksum", normalizeFilename(t.Name()))
}

var checksumFilenameRegexp = regexp.MustCompile("[^A-z0-9]")

// replaces invalid characters on filenames with "-"
func normalizeFilename(f string) string {
	return checksumFilenameRegexp.ReplaceAllString(f, "-")
}
