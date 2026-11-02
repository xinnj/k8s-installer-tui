package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

const testKubesprayArchive = "kubespray-test.tar.gz"

// prepareKubespray must rebuild a tree that carries a patch this build no
// longer ships. The patches are full-file replacements: copying the current
// set over an old tree would leave the old file in place, and a role would
// keep running code that is no longer part of the installer.
func TestPrepareKubesprayRebuildsATreeWithStalePatches(t *testing.T) {
	appDir := t.TempDir()
	useKubesprayPaths(t, appDir)

	stalePatch := filepath.Join("roles", "download", "tasks", "old_patch.yml")
	currentPatch := filepath.Join("roles", "download", "tasks", "download_file.yml")
	writeTestFile(t, filepath.Join(appDir, "patches", currentPatch), "patched")
	writeTestArchive(t, filepath.Join(appDir, testKubesprayArchive), map[string]string{
		filepath.Join("kubespray-test", stalePatch):   "upstream",
		filepath.Join("kubespray-test", currentPatch): "upstream",
	})
	writeTestFile(t, filepath.Join(appDir, "kubespray", stalePatch), "left by an older build")

	prepareKubespray()

	if got := readTestFile(t, filepath.Join(appDir, "kubespray", stalePatch)); got != "upstream" {
		t.Errorf("the stale patch file = %q, want the archive's own file", got)
	}
	if got := readTestFile(t, filepath.Join(appDir, "kubespray", currentPatch)); got != "patched" {
		t.Errorf("the patched file = %q, want this build's patch", got)
	}
}

// A new installer build can ship another kubespray archive with the same
// patches. The tree has to be rebuilt from that archive; matching only the
// patches would leave the old kubespray running under the new binary.
func TestPrepareKubesprayRebuildsWhenTheArchiveChanges(t *testing.T) {
	appDir := t.TempDir()
	useKubesprayPaths(t, appDir)

	patchedFile := filepath.Join("roles", "download", "tasks", "download_file.yml")
	writeTestFile(t, filepath.Join(appDir, "patches", patchedFile), "patched")

	archivePath := filepath.Join(appDir, testKubesprayArchive)
	writeTestArchive(t, archivePath, map[string]string{
		filepath.Join("kubespray-test", "marker"): "old archive",
	})
	prepareKubespray()

	if err := os.Remove(archivePath); err != nil {
		t.Fatal(err)
	}
	writeTestArchive(t, archivePath, map[string]string{
		filepath.Join("kubespray-test", "marker"): "new archive, another size",
	})
	prepareKubespray()

	if got := readTestFile(t, filepath.Join(appDir, "kubespray", "marker")); got != "new archive, another size" {
		t.Errorf("the tree still has %q, want the new archive's file", got)
	}
}

// A tree already built with this build's patches is left alone: a second start
// has no reason to extract or overwrite anything, and does not need the
// archive to be there.
func TestPrepareKubesprayLeavesAStampedTreeAlone(t *testing.T) {
	appDir := t.TempDir()
	useKubesprayPaths(t, appDir)

	patchedFile := filepath.Join("roles", "download", "tasks", "download_file.yml")
	writeTestFile(t, filepath.Join(appDir, "patches", patchedFile), "patched")
	writeTestArchive(t, filepath.Join(appDir, testKubesprayArchive), map[string]string{
		filepath.Join("kubespray-test", patchedFile): "upstream",
	})

	prepareKubespray()
	if err := os.Remove(filepath.Join(appDir, testKubesprayArchive)); err != nil {
		t.Fatal(err)
	}

	prepareKubespray()

	if got := readTestFile(t, filepath.Join(appDir, "kubespray", patchedFile)); got != "patched" {
		t.Errorf("the tree file = %q, want the patch applied by the first start", got)
	}
}

// useKubesprayPaths points prepareKubespray at a test directory and restores
// the globals afterwards.
func useKubesprayPaths(t *testing.T, appDir string) {
	t.Helper()

	originalAppPath, originalKubesprayPath := appPath, kubesprayPath
	appPath = appDir
	kubesprayPath = filepath.Join(appDir, "kubespray")
	t.Cleanup(func() {
		appPath, kubesprayPath = originalAppPath, originalKubesprayPath
	})
}

func writeTestArchive(t *testing.T, path string, files map[string]string) {
	t.Helper()

	archive, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(archive)
	tarWriter := tar.NewWriter(gzipWriter)

	for name, content := range files {
		header := &tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}
