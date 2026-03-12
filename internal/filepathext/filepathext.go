package filepathext

import (
	"os"
	"path/filepath"
	"strings"
)

// JoinDirs joins a list of directory components, scanning right-to-left
// to find the rightmost absolute path, and joining from there forward.
// This allows later (more specific) absolute paths to override earlier ones.
func JoinDirs(dirs []string) string {
	for i := len(dirs) - 1; i >= 0; i-- {
		if i == 0 || filepath.IsAbs(dirs[i]) {
			return filepath.Join(dirs[i:]...)
		}
	}
	return ""
}

// SmartJoin joins two paths, but only if the second is not already an
// absolute path.
func SmartJoin(a, b string) string {
	if IsAbs(b) {
		return b
	}
	return filepath.Join(a, b)
}

func IsAbs(path string) bool {
	// NOTE(@andreynering): If the path contains any if the special
	// variables that we know are absolute, return true.
	if isSpecialDir(path) {
		return true
	}

	return filepath.IsAbs(path)
}

var knownAbsDirs = []string{
	".ROOT_DIR",
	".TASKFILE_DIR",
	".USER_WORKING_DIR",
}

func isSpecialDir(dir string) bool {
	for _, d := range knownAbsDirs {
		if strings.Contains(dir, d) {
			return true
		}
	}
	return false
}

// TryAbsToRel tries to convert an absolute path to relative based on the
// process working directory. If it can't, it returns the absolute path.
func TryAbsToRel(abs string) string {
	wd, err := os.Getwd()
	if err != nil {
		return abs
	}

	rel, err := filepath.Rel(wd, abs)
	if err != nil {
		return abs
	}

	return rel
}

// IsExtOnly checks whether path points to a file with no name but with
// an extension, i.e. ".yaml"
func IsExtOnly(path string) bool {
	return filepath.Base(path) == filepath.Ext(path)
}
