package fingerprint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/wallix/task/v3/taskfile/ast"
)

// ChecksumChecker validates if a task is up to date by calculating its source
// files checksum. It is bound to a single task and precomputes source
// metadata at construction time.
type ChecksumChecker struct {
	tempDir      string
	task         *ast.Task
	sourcesGlobs []*ast.Glob
	srcData      []string
	sourceHash   string // from t.SourceHash or lazily computed; empty if no sources

	// preExecDiskHash is a snapshot of the sources checksum taken at
	// IsUpToDate() time (before execution). SourcesChanged() compares
	// it against a fresh disk computation to detect drift.
	preExecDiskHash string
}

// NewChecksumChecker creates a checker bound to the given task. It
// precomputes the source globs and metadata but does not access disk
// for the source hash — it reuses t.SourceHash when available (set
// during compilation). If t.SourceHash is empty, SourceValue() will
// compute it lazily on first call.
func NewChecksumChecker(tempDir string, t *ast.Task) *ChecksumChecker {
	c := &ChecksumChecker{
		tempDir:    tempDir,
		task:       t,
		sourceHash: t.SourceHash,
	}
	c.sourcesGlobs, c.srcData = c.buildChecksumData()
	return c
}

func serializeCmd(idx int, c *ast.Cmd) string {
	return fmt.Sprintf("cmd[%d]:%s", idx, c.Cmd)
}

func (c *ChecksumChecker) buildChecksumData() ([]*ast.Glob, []string) {
	t := c.task
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
	for i, cmd := range t.RawCmds {
		if cmd == nil {
			continue
		}
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

// TaskStatus holds the fingerprint state and up-to-date status for a task.
type TaskStatus struct {
	Task              string   `json:"task"`
	UpToDate          bool     `json:"up_to_date"`
	SourcesUpToDate   bool     `json:"sources_up_to_date"`
	GeneratesUpToDate bool     `json:"generates_up_to_date"`
	ChecksumFile      string   `json:"checksum_file"`
	SourcesHash       string   `json:"sources_hash,omitempty"`
	SourceFiles       []string `json:"source_files,omitempty"`
	SourceData        []string `json:"source_data,omitempty"`
	GeneratesHash     string   `json:"generates_hash,omitempty"`
	GenerateFiles     []string `json:"generate_files,omitempty"`
}

func (c *ChecksumChecker) IsUpToDate() (bool, error) {
	if len(c.task.Sources) == 0 {
		return false, nil
	}

	// Compute sources hash from current disk state. This is also
	// saved as a snapshot so SourcesChanged() can detect files
	// modified during execution.
	currentSourcesHash, err := c.sourcesChecksum()
	if err != nil {
		return false, err
	}
	c.preExecDiskHash = currentSourcesHash

	checksumFile := c.checksumFilePath()

	data, _ := os.ReadFile(checksumFile)
	oldHashes := strings.TrimSpace(string(data))
	oldSourcesHash, oldGeneratesHash, _ := strings.Cut(oldHashes, "\n")

	newGeneratesHash, err := c.generatesChecksum()
	if err != nil {
		return false, err
	}

	return oldSourcesHash == currentSourcesHash && oldGeneratesHash == newGeneratesHash, nil
}

// SourcesChanged reports whether source files were modified during task
// execution by comparing the disk snapshot taken at IsUpToDate() time
// against the current state.
func (c *ChecksumChecker) SourcesChanged() (bool, error) {
	if c.preExecDiskHash == "" {
		return false, nil
	}
	currentHash, err := c.sourcesChecksum()
	if err != nil {
		return false, err
	}
	return currentHash != c.preExecDiskHash, nil
}

// Status returns the full fingerprint state for a task including
// which parts (sources, generates) are up to date.
func (c *ChecksumChecker) Status() (*TaskStatus, error) {
	checksumFile := c.checksumFilePath()

	data, _ := os.ReadFile(checksumFile)
	oldHashes := strings.TrimSpace(string(data))
	oldSourcesHash, oldGeneratesHash, _ := strings.Cut(oldHashes, "\n")

	currentSourcesHash, err := c.sourcesChecksum()
	if err != nil {
		return nil, err
	}

	newGeneratesHash, err := c.generatesChecksum()
	if err != nil {
		return nil, err
	}

	sourcesFiles, _ := Globs(c.task.ComputeDir(), c.sourcesGlobs)
	generates, _ := Globs(c.task.ComputeDir(), c.task.Generates)

	srcOK := oldSourcesHash == currentSourcesHash
	genOK := oldGeneratesHash == newGeneratesHash

	return &TaskStatus{
		Task:              c.task.Name(),
		UpToDate:          srcOK && genOK,
		SourcesUpToDate:   srcOK,
		GeneratesUpToDate: genOK,
		ChecksumFile:      checksumFile,
		SourcesHash:       oldSourcesHash,
		SourceFiles:       sourcesFiles,
		SourceData:        c.srcData,
		GeneratesHash:     oldGeneratesHash,
		GenerateFiles:     generates,
	}, nil
}

// SourceValue returns the sources checksum for use as the CHECKSUM
// template variable, lock keys, and cache keys. If the hash was not
// precomputed (e.g. during compilation when t.SourceHash is not yet
// set), it is computed from disk on first call and cached.
func (c *ChecksumChecker) SourceValue() string {
	if c.sourceHash == "" && len(c.task.Sources) > 0 {
		c.sourceHash, _ = c.sourcesChecksum()
	}
	return c.sourceHash
}

func (c *ChecksumChecker) SetUpToDate() error {
	if len(c.task.Sources) == 0 {
		return nil
	}

	newSourcesHash, err := c.sourcesChecksum()
	if err != nil {
		return err
	}

	newGeneratesHash, err := c.generatesChecksum()
	if err != nil {
		return err
	}

	checksumFile := c.checksumFilePath()
	_ = os.MkdirAll(filepath.Join(c.tempDir, "checksum"), 0o755)
	if err = os.WriteFile(checksumFile, []byte(newSourcesHash+"\n"+newGeneratesHash+"\n"), 0o644); err != nil {
		return err
	}

	return nil
}

func (c *ChecksumChecker) OnError() error {
	if len(c.task.Sources) == 0 {
		return nil
	}
	return os.Remove(c.checksumFilePath())
}

func (*ChecksumChecker) Kind() string {
	return "checksum"
}

// sourcesChecksum computes the current sources hash from disk.
func (c *ChecksumChecker) sourcesChecksum() (string, error) {
	return c.checksum(c.sourcesGlobs, c.srcData)
}

// generatesChecksum computes the current generates hash from disk.
func (c *ChecksumChecker) generatesChecksum() (string, error) {
	return c.checksum(c.task.Generates, nil)
}

func (c *ChecksumChecker) checksum(globs []*ast.Glob, data []string) (string, error) {
	sources, err := Globs(c.task.ComputeDir(), globs)
	if err != nil {
		return "", err
	}
	return ChecksumFiles(c.task.ComputeDir(), sources, data)
}

func (c *ChecksumChecker) checksumFilePath() string {
	return filepath.Join(c.tempDir, "checksum", normalizeFilename(c.task.Name()))
}

var checksumFilenameRegexp = regexp.MustCompile("[^A-z0-9]")

// replaces invalid characters on filenames with "-"
func normalizeFilename(f string) string {
	return checksumFilenameRegexp.ReplaceAllString(f, "-")
}
