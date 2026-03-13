package fingerprint

import (
	"os"
	"sort"

	"github.com/wallix/task/v3/internal/execext"
	"github.com/wallix/task/v3/internal/filepathext"
	"github.com/wallix/task/v3/taskfile/ast"
)

// Globs expands glob patterns and returns matching files. For generates
// entries with a Fingerprint field, only the fingerprint file is returned
// (used for checksum-based up-to-date detection).
func Globs(dir string, globs []*ast.Glob) ([]string, error) {
	resultMap := make(map[string]bool)
	for _, g := range globs {
		if g.Fingerprint != "" {
			// Use only the fingerprint file for hashing
			fp := filepathext.SmartJoin(dir, g.Fingerprint)
			if _, err := os.Stat(fp); err == nil {
				resultMap[fp] = !g.Negate
			}
			continue
		}
		matches, err := Glob(dir, g.Glob)
		if err != nil {
			continue
		}
		for _, match := range matches {
			resultMap[match] = !g.Negate
		}
	}
	return collectKeys(resultMap), nil
}

// CacheGlobs expands glob patterns for cache operations. Unlike Globs,
// it always uses the full glob pattern (ignoring Fingerprint), so that
// cache archives contain all generated files. When a Fingerprint is set,
// the fingerprint file is also included (it may not match the glob, e.g.
// dotfiles are not matched by shell **/* patterns).
func CacheGlobs(dir string, globs []*ast.Glob) ([]string, error) {
	resultMap := make(map[string]bool)
	for _, g := range globs {
		matches, err := Glob(dir, g.Glob)
		if err != nil {
			continue
		}
		for _, match := range matches {
			resultMap[match] = !g.Negate
		}
		// Ensure the fingerprint file is included in cache even if
		// the glob doesn't match it (e.g. dotfiles vs **/*).
		if g.Fingerprint != "" && !g.Negate {
			fp := filepathext.SmartJoin(dir, g.Fingerprint)
			if _, err := os.Stat(fp); err == nil {
				resultMap[fp] = true
			}
		}
	}
	return collectKeys(resultMap), nil
}

func Glob(dir string, g string) ([]string, error) {
	g = filepathext.SmartJoin(dir, g)

	fs, err := execext.ExpandFields(g)
	if err != nil {
		return nil, err
	}

	results := make(map[string]bool, len(fs))

	for _, f := range fs {
		info, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			continue
		}
		results[f] = true
	}
	return collectKeys(results), nil
}

func collectKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
