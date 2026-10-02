// Package gitidentity checks the current checkout without executing stored paths.
package gitidentity

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tools-programmer/internal/core"
)

type Result struct {
	Origin    string `json:"-"` // Private evidence for detecting changes between validations.
	Project   string `json:"project"`
	Checkout  string `json:"checkout"`
	Author    string `json:"author"`
	Committer string `json:"committer"`
	Matches   bool   `json:"matches"`
}

type boundedOutput struct{ buffer bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 65536 {
		return 0, core.Fail("git_io", "Git output exceeded bound")
	}
	return b.buffer.Write(p)
}

func run(ctx context.Context, args ...string) (string, error) {
	return runAt(ctx, "", args...)
}

func runAt(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	var out, stderr boundedOutput
	cmd.Stdout, cmd.Stderr = &out, &stderr
	// Inherit identity and config overrides exactly as the subsequent commit would.
	if err := cmd.Run(); err != nil {
		return "", core.Fail("git_io", "read-only Git inspection failed; stderr suppressed")
	}
	return strings.TrimSuffix(out.buffer.String(), "\n"), nil
}

func remoteRepo(raw string) (string, error) {
	return core.OriginRepo(raw)
}

func Check(ctx context.Context, p core.Project) (Result, error) {
	return checkAt(ctx, p, "")
}

// CheckAt inspects an explicit target using the same identity and origin policy as Check.
// The caller supplies a pathless copy when validating a replacement checkout.
func CheckAt(ctx context.Context, p core.Project, dir string) (Result, error) {
	if !filepath.IsAbs(dir) {
		return Result{Project: p.Name}, core.Fail("invalid_input", "checkout path must be absolute")
	}
	return checkAt(ctx, p, dir)
}

func checkAt(ctx context.Context, p core.Project, dir string) (Result, error) {
	r := Result{Project: p.Name}
	if err := p.ValidateIdentity(); err != nil {
		return r, err
	}
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE"} {
		if _, set := os.LookupEnv(key); set {
			return r, core.Fail("checkout_mismatch", "Git repository redirection environment is unsupported for identity checks")
		}
	}
	root, err := runAt(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return r, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(root) {
		return r, core.Fail("checkout_mismatch", "cannot resolve checkout root")
	}
	r.Checkout = root
	if p.Path != "" {
		stored, err := filepath.EvalSymlinks(p.Path)
		if err != nil || !filepath.IsAbs(stored) || stored != root {
			return r, core.Fail("checkout_mismatch", "current checkout does not match registered path")
		}
	}
	raw, err := runAt(ctx, dir, "config", "--local", "--get-all", "remote.origin.url")
	if err != nil {
		return r, err
	}
	if !p.MatchesOrigin(raw) {
		return r, core.Fail("checkout_mismatch", "checkout origin does not match registered repository")
	}
	r.Origin = raw
	ident := regexp.MustCompile(`^(.+) <([^<>]+)> [0-9]+ [+-][0-9]{4}$`)
	for _, item := range []struct {
		variable string
		target   *string
	}{{"GIT_AUTHOR_IDENT", &r.Author}, {"GIT_COMMITTER_IDENT", &r.Committer}} {
		value, err := runAt(ctx, dir, "var", item.variable)
		if err != nil {
			return r, err
		}
		parts := ident.FindStringSubmatch(value)
		if len(parts) != 3 {
			return r, core.Fail("identity_mismatch", "Git returned malformed effective identity")
		}
		*item.target = parts[1] + " <" + parts[2] + ">"
		if parts[1] != p.GitName || parts[2] != p.GitEmail {
			return r, core.Fail("identity_mismatch", "effective Git author or committer differs from registered name/email; no configuration changes are performed")
		}
	}
	r.Matches = true
	return r, nil
}
