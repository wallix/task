package fingerprint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-task/task/v3/taskfile/ast"
)

// ChecksumChecker validates if a task is up to date by calculating its source
// files checksum
type ChecksumChecker struct {
	tempDir string
}

func NewChecksumChecker(tempDir string) *ChecksumChecker {
	return &ChecksumChecker{
		tempDir: tempDir,
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

func (checker *ChecksumChecker) IsUpToDate(t *ast.Task) (bool, string, error) {
	if len(t.Sources) == 0 && len(t.Generates) == 0 {
		return false, "", nil
	}

	checksumFile := checker.checksumFilePath(t)

	data, _ := os.ReadFile(checksumFile)
	oldHashes := strings.TrimSpace(string(data))
	oldSourcesHash, oldGeneratesHash, _ := strings.Cut(oldHashes, "\n")

	sourcesGlobs, srcData := checker.filterChecksumData(t)
	newSourcesHash, err := checker.checksum(t, sourcesGlobs, srcData)
	if err != nil {
		return false, "", err
	}

	newGeneratesHash, err := checker.checksum(t, t.Generates, nil)
	if err != nil {
		return false, "", err
	}

	return oldSourcesHash == newSourcesHash && oldGeneratesHash == newGeneratesHash, newSourcesHash, nil
}

func (checker *ChecksumChecker) Value(t *ast.Task) (any, error) {
	sourcesGlobs, srcData := checker.filterChecksumData(t)
	c1, err := checker.checksum(t, sourcesGlobs, srcData)
	if err != nil {
		return c1, err
	}
	c2, err := checker.checksum(t, t.Generates, nil)
	return c1 + "\n" + c2, err
}

func (checker *ChecksumChecker) SetUpToDate(t *ast.Task, sourceHash string) error {
	if len(t.Sources) == 0 && len(t.Generates) == 0 {
		return nil
	}

	sourcesGlobs, srcData := checker.filterChecksumData(t)
	newSourcesHash, err := checker.checksum(t, sourcesGlobs, srcData)
	if err != nil {
		return err
	}

	checksumFile := checker.checksumFilePath(t)

	if sourceHash != "" && newSourcesHash != sourceHash {
		// Sources changed during execution — remove checksum file since the
		// next execution will have a different checksum
		os.Remove(checksumFile)
		return nil
	}

	newGeneratesHash, err := checker.checksum(t, t.Generates, nil)
	if err != nil {
		return err
	}

	_ = os.MkdirAll(filepath.Join(checker.tempDir, "checksum"), 0o755)
	if err = os.WriteFile(checksumFile, []byte(newSourcesHash+"\n"+newGeneratesHash+"\n"), 0o644); err != nil {
		return err
	}

	return nil
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
