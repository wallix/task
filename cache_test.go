package task

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestExtractZipEntryRejectsEscapes: a malicious cache archive must not
// write outside the extraction root — neither through a non-local entry
// name nor through a symlink extracted by an earlier entry.
func TestExtractZipEntryRejectsEscapes(t *testing.T) {
	outside := t.TempDir()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"../evil", "/abs"} {
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	link := &zip.FileHeader{Name: "exit"}
	link.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(outside)); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"exit/pwned": "pwned", "sub/ok": "ok"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	wantErr := map[string]bool{"../evil": true, "/abs": true, "exit/pwned": true}
	for _, entry := range zr.File {
		err := extractZipEntry(dir, entry)
		if wantErr[entry.Name] && err == nil {
			t.Errorf("%s: expected a rejection", entry.Name)
		}
		if !wantErr[entry.Name] && err != nil {
			t.Errorf("%s: %v", entry.Name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(outside, "pwned")); !os.IsNotExist(err) {
		t.Error("extraction escaped the root through the symlink")
	}
	if got, err := os.ReadFile(filepath.Join(dir, "sub", "ok")); err != nil || string(got) != "ok" {
		t.Errorf("benign entry not extracted: %q, %v", got, err)
	}
}
