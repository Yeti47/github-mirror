// Package git provides a thin wrapper around the git CLI for mirror operations.
// Authentication is injected per-invocation via url.insteadOf rewriting so the PAT
// is never persisted to the repository's on-disk config.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/Yeti47/github-mirror/internal/config"
)

// Runner executes git CLI commands for mirror operations.
// All git operations use per-invocation PAT injection via url.insteadOf rewriting.
type Runner struct {
	pat config.PersonalAccessToken
}

// NewRunner creates a new Runner that authenticates with the given PAT.
func NewRunner(pat config.PersonalAccessToken) *Runner {
	return &Runner{pat: pat}
}

// CloneMirror performs a bare mirror clone of the repository at cloneURL into targetDir.
// The resulting clone has all refs and is suitable for backup and restore.
func (r *Runner) CloneMirror(ctx context.Context, cloneURL, targetDir string) error {
	return r.run(ctx, "", "clone", "--mirror", cloneURL, targetDir)
}

// RemoteUpdate fetches all refs from all remotes and prunes deleted ones.
// It must be called on an existing mirror clone directory.
func (r *Runner) RemoteUpdate(ctx context.Context, dir string) error {
	return r.run(ctx, dir, "remote", "update", "--prune")
}

// FetchLFS fetches all Git LFS objects for the mirror at dir.
// Returns an error if the git-lfs command fails; callers should treat this as non-fatal
// for repos that do not use LFS.
func (r *Runner) FetchLFS(ctx context.Context, dir string) error {
	return r.run(ctx, dir, "lfs", "fetch", "--all")
}

// run executes a git command with PAT auth injected via url.insteadOf rewriting.
// If dir is non-empty, git runs in that directory (-C <dir>).
// The PAT is never passed as a command-line argument; it lives only in the
// url.insteadOf config value which is not logged and is never persisted.
func (r *Runner) run(ctx context.Context, dir string, args ...string) error {
	insteadOf := fmt.Sprintf("url.https://oauth2:%s@github.com/.insteadOf=https://github.com/", r.pat.Value())
	fullArgs := make([]string, 0, len(args)+2)
	fullArgs = append(fullArgs, "-c", insteadOf)
	fullArgs = append(fullArgs, args...)

	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	if dir != "" {
		cmd.Dir = dir
	}

	// Inherit the process environment but override git-specific vars to
	// suppress interactive prompts and global config side-effects.
	env := os.Environ()
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/true",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
	cmd.Env = env

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := r.pat.RedactAllOccurrencesIn(stderr.String())
		if len(msg) > 512 {
			msg = msg[:512] + "…"
		}
		return fmt.Errorf("git %s: %w — stderr: %s", args[0], err, msg)
	}
	return nil
}
