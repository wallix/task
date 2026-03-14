package task

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wallix/task/v3/internal/fingerprint"
	"github.com/wallix/task/v3/internal/logger"
)

// runSetupForCalls runs setup tasks for each call, ensuring preparation
// steps (like version enforcement) execute before cache operations.
func (e *Executor) runSetupForCalls(ctx context.Context, calls ...*Call) error {
	for _, call := range calls {
		t, err := e.CompiledTask(call)
		if err != nil {
			return err
		}
		if err := e.runSetup(ctx, t); err != nil {
			return err
		}
		if err := e.mergeSetupFingerprints(t); err != nil {
			return err
		}
	}
	return nil
}

// ExportCache exports the fingerprint state (checksum files + generated files)
// for the given tasks and all their dependencies to a ZIP archive.
// Setup tasks are run first so their outputs exist and fingerprint state is accurate.
// Only tasks that are up-to-date are included. The ZIP stores paths relative
// to the executor's working directory.
func (e *Executor) ExportCache(zipPath string, calls ...*Call) error {
	ctx := context.Background()
	if err := e.runSetupForCalls(ctx, calls...); err != nil {
		return err
	}

	// Collect all files to export by walking the task dependency tree
	exportFiles := make(map[string]string) // path -> task name (for dedup)
	if err := e.collectCacheFiles(exportFiles, calls...); err != nil {
		return err
	}

	if len(exportFiles) == 0 {
		e.Logger.Errf(logger.Yellow, "task: no up-to-date tasks to export\n")
		return nil
	}

	// Sort files for deterministic output
	files := make([]string, 0, len(exportFiles))
	for f := range exportFiles {
		files = append(files, f)
	}
	slices.Sort(files)

	// Check if existing archive is identical
	if archiveMatches(e.Dir, zipPath, files) {
		e.Logger.Outf(logger.Magenta, "task: cache %q is unmodified\n", zipPath)
		return nil
	}

	e.Logger.Outf(logger.Magenta, "task: exporting cache to %q\n", zipPath)

	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("task: failed to create cache file: %w", err)
	}
	defer zipFile.Close()

	zw := zip.NewWriter(zipFile)
	defer zw.Close()

	for _, f := range files {
		if err := addFileToZip(zw, e.Dir, f); err != nil {
			return fmt.Errorf("task: failed to add %s to cache: %w", f, err)
		}
	}

	return nil
}

// ImportCache restores files from a ZIP archive created by ExportCache.
// It extracts all files to the executor's working directory, preserving
// permissions, modification times, and symlinks. Setup tasks are run
// after extraction so preparation steps are applied.
func (e *Executor) ImportCache(zipPath string, calls ...*Call) error {
	f, err := os.Open(zipPath)
	if err != nil {
		return fmt.Errorf("task: failed to open cache file: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("task: failed to stat cache file: %w", err)
	}

	zr, err := zip.NewReader(f, stat.Size())
	if err != nil {
		return fmt.Errorf("task: failed to read cache file: %w", err)
	}

	e.Logger.Outf(logger.Magenta, "task: importing cache from %q\n", zipPath)

	for _, entry := range zr.File {
		if err := extractZipEntry(e.Dir, entry); err != nil {
			e.Logger.Errf(logger.Red, "task: failed to extract %s: %v\n", entry.Name, err)
		}
	}

	// Run setup tasks after import so preparation steps are applied
	ctx := context.Background()
	return e.runSetupForCalls(ctx, calls...)
}

// collectCacheFiles collects checksum files and generated files for the
// given tasks. Generated files from dependencies are resolved at compile
// time via "from: deps" / "from: cmds" directives, so no recursive walk
// is needed here.
func (e *Executor) collectCacheFiles(files map[string]string, calls ...*Call) error {
	for _, call := range calls {
		t, err := e.CompiledTask(call)
		if err != nil {
			return err
		}

		if len(t.Sources) == 0 && len(t.Generates) == 0 {
			continue
		}

		st, err := fingerprint.NewChecksumChecker(e.TempDir.Fingerprint, t).Status()
		if err != nil {
			return err
		}

		if !st.UpToDate {
			e.Logger.Errf(logger.Yellow, "task: %q not up to date, skipped from export\n", t.Name())
			continue
		}

		if st.ChecksumFile != "" {
			if existing := files[st.ChecksumFile]; existing != "" {
				e.Logger.Errf(logger.Yellow, "task: checksum %q used by both %q and %q\n", st.ChecksumFile, existing, t.Name())
			} else {
				files[st.ChecksumFile] = t.Name()
			}
		}
		for _, f := range st.CacheFiles {
			files[f] = t.Name()
		}
	}
	return nil
}

// addFileToZip adds a single file (or symlink) to a ZIP writer.
// Paths are stored relative to baseDir.
func addFileToZip(zw *zip.Writer, baseDir, filePath string) error {
	info, err := os.Lstat(filePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}

	// Store relative path — reject files outside baseDir to prevent
	// archive entries that would escape the extraction directory.
	rel, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		return fmt.Errorf("cannot relativize %s to %s: %w", filePath, baseDir, err)
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("file %s is outside project root %s", filePath, baseDir)
	}
	header.Name = rel

	isSymlink := info.Mode().Type()&os.ModeSymlink != 0
	if isSymlink {
		header.Method = zip.Store
	} else {
		header.Method = zip.Deflate
	}

	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}

	if isSymlink {
		link, err := os.Readlink(filePath)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(link))
		return err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// extractZipEntry extracts a single entry from a ZIP archive to baseDir.
func extractZipEntry(baseDir string, entry *zip.File) error {
	path := filepath.Join(baseDir, entry.Name)

	if entry.FileInfo().IsDir() {
		return os.MkdirAll(path, entry.Mode())
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	src, err := entry.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	srcData, err := io.ReadAll(src)
	if err != nil {
		return err
	}

	isSymlink := entry.Mode()&os.ModeSymlink != 0

	// Check if file already exists with identical content
	if existingInfo, err := os.Lstat(path); err == nil {
		existingIsSymlink := existingInfo.Mode()&os.ModeSymlink != 0
		if existingIsSymlink == isSymlink {
			if isSymlink {
				if link, err := os.Readlink(path); err == nil && link == string(srcData) {
					return nil // identical symlink
				}
			} else {
				if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, srcData) {
					// Identical content — just restore metadata
					_ = os.Chmod(path, entry.Mode())
					_ = os.Chtimes(path, entry.Modified, entry.Modified)
					return nil
				}
			}
		}
		// Remove existing before replacing
		os.Remove(path)
	}

	if isSymlink {
		return os.Symlink(string(srcData), path)
	}

	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, entry.Mode())
	if err != nil {
		return err
	}
	if _, err := dst.Write(srcData); err != nil {
		dst.Close()
		return err
	}
	dst.Close()

	return os.Chtimes(path, entry.Modified, entry.Modified)
}

// archiveMatches checks if an existing ZIP archive contains exactly the
// same files with the same content as the given file list. Used to avoid
// re-exporting unchanged caches.
func archiveMatches(baseDir, zipPath string, files []string) bool {
	st, err := os.Stat(zipPath)
	if err != nil {
		return false
	}

	f, err := os.Open(zipPath)
	if err != nil {
		return false
	}
	defer f.Close()

	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return false
	}

	seen := make(map[string]bool, len(files))
	for _, file := range files {
		seen[file] = false
	}

	for _, entry := range zr.File {
		fpath := filepath.Join(baseDir, entry.Name)
		if _, ok := seen[fpath]; !ok {
			return false // extraneous file in zip
		}

		isSymlink := entry.Mode()&os.ModeSymlink != 0

		var diskData []byte
		if isSymlink {
			link, err := os.Readlink(fpath)
			if err != nil {
				return false
			}
			diskData = []byte(link)
		} else {
			var err error
			diskData, err = os.ReadFile(fpath)
			if err != nil {
				return false
			}
		}

		r, err := entry.Open()
		if err != nil {
			return false
		}
		zipData, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return false
		}

		if !bytes.Equal(diskData, zipData) {
			return false
		}
		seen[fpath] = true
	}

	// Check all expected files were found
	for f, found := range seen {
		if !found {
			if fi, err := os.Stat(f); err == nil && fi.IsDir() {
				continue
			}
			return false
		}
	}
	return true
}

// StatusExport combines status display with cache export. It is an alternative
// entry point used by the CLI when --status and --export-cache are combined.
func (e *Executor) StatusExport(zipPath string, calls ...*Call) error {
	// First show status
	if err := e.Status(calls...); err != nil {
		return err
	}
	// Then export
	return e.ExportCache(zipPath, calls...)
}
