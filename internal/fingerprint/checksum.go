package fingerprint

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zeebo/xxh3"
)

// ChecksumFiles computes an xxh3 hash over the given files and data strings.
// File paths are hashed as relative paths from basedir. Symlinks are hashed
// by their link target rather than the file content they point to.
func ChecksumFiles(basedir string, files []string, data []string) (string, error) {
	h := xxh3.New()
	buf := make([]byte, 128*1024)
	for _, f := range files {
		// Hash the relative path so checksum changes when a file is moved
		if rel, err := filepath.Rel(basedir, f); err == nil {
			_, _ = h.WriteString(rel)
		} else {
			_, _ = h.WriteString(f)
		}
		// Hash symlink targets rather than the content they point to
		if fi, err := os.Lstat(f); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(f)
			if err != nil {
				return "", err
			}
			_, _ = h.WriteString(link)
		} else {
			f, err := os.Open(f)
			if err != nil {
				return "", err
			}
			_, err = io.CopyBuffer(h, f, buf)
			f.Close()
			if err != nil {
				return "", err
			}
		}
	}
	for _, d := range data {
		_, _ = h.WriteString(d)
	}
	hash := h.Sum128()
	return fmt.Sprintf("%x%x", hash.Hi, hash.Lo), nil
}
