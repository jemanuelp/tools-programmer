package lab

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSourceTransferPreservesPrivateModesAndIdentity(t *testing.T) {
	root := snapshotFixture(t)
	if err := os.Chmod(filepath.Join(root, "file.go"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := t.TempDir()
	s, err := Snapshot(context.Background(), root, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := WriteSourceArchive(snapshot, s, &archive); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "source")
	if err := receiveSource(dest, &archive); err != nil {
		t.Fatal(err)
	}
	if err := VerifySource(dest, s); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dest, "file.go"))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, err)
	}
	st, err = os.Stat(dest)
	if err != nil || st.Mode().Perm() != 0700 {
		t.Fatal(st, err)
	}
}

func TestSourceTransferRejectsUnsafeArchive(t *testing.T) {
	for _, h := range []tar.Header{{Name: "../escape", Typeflag: tar.TypeReg}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, {Name: "special", Typeflag: tar.TypeFifo}, {Name: "file.go", Mode: 04755, Typeflag: tar.TypeReg}} {
		t.Run(h.Name, func(t *testing.T) {
			var b bytes.Buffer
			w := tar.NewWriter(&b)
			if err := w.WriteHeader(&h); err != nil {
				t.Fatal(err)
			}
			w.Close()
			if err := receiveSource(filepath.Join(t.TempDir(), "source"), &b); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func sourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// No commits are created: a loose synthetic commit is unnecessary for path/hash tests.
	c := exec.Command("git", "init", "-q", root)
	c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return root
}

// A deterministic fake Git boundary supplies metadata without creating commits
// or consulting a developer's repository/configuration during these tests.
func snapshotFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n*'rev-parse HEAD') printf '%s\\n' '" + strings.Repeat("b", 40) + "';;\n*'ls-files'*) printf 'file.go\\0';;\n*'status'*) printf '?? file.go\\n';;\n*) exit 1;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(root, "file.go"), []byte("package fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// This fixture explicitly tests 0644 preservation, independent of the caller's umask.
	if err := os.Chmod(filepath.Join(root, "file.go"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestSnapshotCopiesDirtyBytesModesAndRejectsExtraFiles(t *testing.T) {
	root := snapshotFixture(t)
	destination := t.TempDir()
	s, err := Snapshot(context.Background(), root, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Dirty || len(s.Files) != 1 || s.Files[0].Mode != 0644 {
		t.Fatal(s)
	}
	if err := VerifySource(destination, s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "unexpected.go"), []byte("package extra"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := VerifySource(destination, s); err == nil {
		t.Fatal("unexpected build input accepted")
	}
}

func TestSnapshotFixtureUnderRestrictiveUmask(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Change umask only in a child process, never in the shared test process.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `umask 077; exec "$1" -test.run '^TestSnapshotCopiesDirtyBytesModesAndRejectsExtraFiles$' -test.count=1`, "umask-fixture", binary)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("snapshot fixture under umask 077: %v\n%s", err, output)
	}
}
func TestSnapshotRejectsConcurrentDrift(t *testing.T) {
	root := snapshotFixture(t)
	gitPath := filepath.Join(strings.Split(os.Getenv("PATH"), ":")[0], "git")
	script := "#!/bin/sh\ncase \"$*\" in\n*'rev-parse HEAD') printf '%s\\n' '" + strings.Repeat("b", 40) + "';;\n*'ls-files'*) if test -f '" + root + "/.count'; then printf 'changed bytes\\n' > '" + root + "/file.go'; else touch '" + root + "/.count'; fi; printf 'file.go\\0';;\n*'status'*) printf '?? file.go\\n';;\n*) exit 1;;\nesac\n"
	if err := os.WriteFile(gitPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Snapshot(context.Background(), root, t.TempDir()); err == nil || err.Error() != "source_changed_during_capture" {
		t.Fatal("drift not detected", err)
	}
}
func TestSourceModeAndDeletionChangeIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.go")
	os.WriteFile(path, []byte("source"), 0644)
	a, _ := scanSource(root, []string{"file.go"}, "")
	os.Chmod(path, 0755)
	b, _ := scanSource(root, []string{"file.go"}, "")
	if a[0].Mode == b[0].Mode {
		t.Fatal("mode ignored")
	}
	os.Remove(path)
	c, err := scanSource(root, []string{"file.go"}, "")
	if err != nil || len(c) != 0 {
		t.Fatal(c, err)
	}
}
func TestSourceIncludesUntrackedAndDetectsModifiedContent(t *testing.T) {
	root := sourceFixture(t)
	p := filepath.Join(root, "new.go")
	if err := os.WriteFile(p, []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	paths, err := sourcePaths(context.Background(), root)
	if err != nil || len(paths) != 1 {
		t.Fatal(paths, err)
	}
	a, err := scanSource(root, paths, "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte("second"), 0644)
	b, err := scanSource(root, paths, "")
	if err != nil || a[0].Hash == b[0].Hash {
		t.Fatal(b, err)
	}
	if err := VerifySource(root, Source{Hash: "wrong", Files: a}); err == nil {
		t.Fatal("accepted stale source")
	}
}
func TestSourceRejectsSecretsAndSymlinks(t *testing.T) {
	for _, p := range []string{"../escape", ".git/config", "x/.env", "state.db", "private.key", "a\nb", "/absolute"} {
		t.Run(p, func(t *testing.T) {
			if safeSourcePath(p) {
				t.Fatal("unsafe path")
			}
		})
	}
	root := t.TempDir()
	os.Symlink(t.TempDir(), filepath.Join(root, "escape"))
	if _, err := scanSource(root, []string{"escape/file"}, ""); err == nil {
		t.Fatal("symlink ancestor accepted")
	}
}
