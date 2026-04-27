// Package e2e contains end-to-end tests that build and run the real Docker
// image against GitHub's API.
//
// Prerequisites:
//   - Docker daemon running and accessible to the current user.
//   - E2E=true environment variable to opt in.
//   - GITHUB_TOKEN set to a PAT with "Contents: Read-only" and
//     "Metadata: Read-only" on "All repositories".
//
// Run with:
//
//	E2E=true GITHUB_TOKEN=ghp_... go test -v -timeout 10m ./e2e/
package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const imageTag = "github-mirror:e2e"

// projectRoot resolves the repository root relative to this test file's
// package directory (e2e → .. → repo root).
func projectRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join(".."))
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	return root
}

// buildImage runs `docker build` targeting the Dockerfile in docker/.
func buildImage(t *testing.T, root string) {
	t.Helper()
	t.Log("building Docker image (this may take a moment on a cold cache)…")
	cmd := exec.Command(
		"docker", "build",
		"-f", filepath.Join(root, "docker", "Dockerfile"),
		"-t", imageTag,
		root,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker build: %v", err)
	}
}

// TestSyncOnce builds the Docker image, runs a single sync cycle via
// --once, and asserts that at least one bare mirror clone was created in
// the mounted mirror directory.
func TestSyncOnce(t *testing.T) {
	if os.Getenv("E2E") != "true" {
		t.Skip("skipping e2e test; set E2E=true to run")
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		t.Skip("GITHUB_TOKEN not set; set it to run e2e tests")
	}

	root := projectRoot(t)
	buildImage(t, root)

	// Temporary host directories that are volume-mounted into the container.
	// 0o777 is required because the container runs as uid 1000 (non-root)
	// while the host dirs are owned by the current user.
	mirrorDir := t.TempDir()
	stateDir := t.TempDir()
	if err := os.Chmod(mirrorDir, 0o777); err != nil {
		t.Fatalf("chmod mirrorDir: %v", err)
	}
	if err := os.Chmod(stateDir, 0o777); err != nil {
		t.Fatalf("chmod stateDir: %v", err)
	}

	t.Logf("mirror dir : %s", mirrorDir)
	t.Logf("state dir  : %s", stateDir)

	cmd := exec.Command(
		"docker", "run", "--rm",
		// Credentials.
		"-e", "GITHUB_TOKEN="+token,
		// Override default paths so they land on our mounted dirs.
		"-e", "MIRROR_DIR=/data",
		"-e", "DB_PATH=/state/state.db",
		// Mount host temp dirs.
		"-v", mirrorDir+":/data",
		"-v", stateDir+":/state",
		// Image and the --once flag (passed to the binary via tini).
		imageTag,
		"--once",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	t.Log("running sync --once…")
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker run --once: %v", err)
	}

	// Assert at least one bare .git clone was written to the mirror dir.
	var clones []string
	err := filepath.WalkDir(mirrorDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && strings.HasSuffix(d.Name(), ".git") {
			clones = append(clones, path)
			return filepath.SkipDir // don't descend into the bare clone
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk mirror dir: %v", err)
	}
	if len(clones) == 0 {
		t.Fatal("expected at least one bare .git clone in the mirror directory; found none")
	}
	t.Logf("mirrored %d repo(s):", len(clones))
	for _, c := range clones {
		t.Logf("  %s", c)
	}

	// Assert the state DB was created (proves the full protocol lifecycle ran).
	dbPath := filepath.Join(stateDir, "state.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("state database was not created at %s", dbPath)
	}
}
