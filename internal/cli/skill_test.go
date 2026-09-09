package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bundle "tools-programmer"
	"tools-programmer/internal/skillsync"
)

func skillRun(t *testing.T, args ...string) (skillsync.Result, int) {
	t.Helper()
	var out bytes.Buffer
	code := Run(context.Background(), args, &out, forbiddenRunner{t})
	var response struct {
		Data skillsync.Result `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(out.String(), err)
	}
	return response.Data, code
}

func assertSkill(t *testing.T, home string) {
	t.Helper()
	for _, name := range []string{"SKILL.md", "references/cli.md"} {
		want, err := bundle.SkillFiles.ReadFile(".opencode/skills/tools-programmer/" + name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(home, ".config/opencode/skills/tools-programmer", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("installed %s differs from bundle: %v", name, err)
		}
	}
}

func TestSkillSyncInstallRepeatAndDrift(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "ignored-config"))
	t.Setenv("XDG_STATE_HOME", "invalid-relative-state")
	t.Setenv("TOOLS_PROGRAMMER_DB", filepath.Join(home, "absent-state/history.db"))
	r, code := skillRun(t, "--db", ":memory:", "skill", "sync", "opencode")
	if code != 0 || len(r.Updated) != 2 || len(r.Unchanged) != 0 || r.Destination != filepath.Join(home, ".config/opencode/skills/tools-programmer") {
		t.Fatalf("fresh sync: %+v, exit %d", r, code)
	}
	assertSkill(t, home)
	skill := filepath.Join(r.Destination, "SKILL.md")
	stamp := time.Unix(123456, 0)
	if err := os.Chtimes(skill, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	r, code = skillRun(t, "skill", "sync", "opencode")
	info, err := os.Stat(skill)
	if code != 0 || len(r.Updated) != 0 || len(r.Unchanged) != 2 || err != nil || !info.ModTime().Equal(stamp) {
		t.Fatalf("not a no-op: %+v, %v", r, err)
	}
	extras := []string{"notes.txt", "references/custom.md", "../other-skill/SKILL.md"}
	for _, name := range extras {
		path := filepath.Join(r.Destination, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(skill, []byte("local customization"), 0600); err != nil {
		t.Fatal(err)
	}
	r, code = skillRun(t, "skill", "sync", "opencode")
	if code != 0 || len(r.Updated) != 1 || r.Updated[0] != "SKILL.md" || len(r.Unchanged) != 1 {
		t.Fatalf("drift sync: %+v, exit %d", r, code)
	}
	assertSkill(t, home)
	for _, name := range extras {
		got, err := os.ReadFile(filepath.Join(r.Destination, name))
		if err != nil || string(got) != "keep" {
			t.Fatal("unrelated file changed", name, err)
		}
	}
	for _, name := range []string{"absent-state", "ignored-config", ".local"} {
		if _, err := os.Lstat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatal("unexpected state", name, err)
		}
	}
}

func TestSkillSyncRejectsArgumentsWithoutEffects(t *testing.T) {
	for _, args := range [][]string{{"skill"}, {"skill", "sync"}, {"skill", "sync", "other"}, {"skill", "sync", "opencode", "extra"}, {"skill", "sync", "opencode", "--help"}, {"skill", "sync", "--project", "demo"}, {"skill", "sync", "../opencode"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if _, code := skillRun(t, args...); code == 0 {
				t.Fatal("invalid arguments accepted")
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid command touched HOME", err)
			}
		})
	}
	for _, home := range []string{"", "relative"} {
		t.Setenv("HOME", home)
		if _, code := skillRun(t, "skill", "sync", "opencode"); code == 0 {
			t.Fatal("invalid HOME accepted")
		}
	}
}

func TestSkillSyncRefusesUnsafePathsBeforeWrites(t *testing.T) {
	paths := []string{".", ".config", ".config/opencode", ".config/opencode/skills", ".config/opencode/skills/tools-programmer", ".config/opencode/skills/tools-programmer/references", ".config/opencode/skills/tools-programmer/SKILL.md", ".config/opencode/skills/tools-programmer/references/cli.md"}
	for _, path := range paths {
		for _, kind := range []string{"symlink", "dangling", "wrong-type"} {
			t.Run(path+"/"+kind, func(t *testing.T) {
				base := t.TempDir()
				home := filepath.Join(base, "home")
				t.Setenv("HOME", home)
				target := filepath.Join(home, path)
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					t.Fatal(err)
				}
				outside := t.TempDir()
				var err error
				if kind == "wrong-type" {
					if filepath.Ext(target) == ".md" {
						err = os.Mkdir(target, 0755)
					} else {
						err = os.WriteFile(target, []byte("keep"), 0600)
					}
				} else {
					link := outside
					if kind == "dangling" {
						link = filepath.Join(outside, "missing")
					}
					err = os.Symlink(link, target)
				}
				if err != nil {
					t.Fatal(err)
				}
				r, code := skillRun(t, "skill", "sync", "opencode")
				if code == 0 || len(r.Updated) != 0 {
					t.Fatalf("unsafe sync: %+v, exit %d", r, code)
				}
				entries, err := os.ReadDir(outside)
				if err != nil || len(entries) != 0 {
					t.Fatal("symlink destination modified", err)
				}
			})
		}
	}
}

func TestBinarySkillSyncStandalone(t *testing.T) {
	if testing.Short() {
		t.Skip("standalone binary subprocess smoke")
	}
	dir := t.TempDir()
	binary := os.Getenv("TOOLS_PROGRAMMER_TEST_BINARY")
	if binary == "" {
		binary = filepath.Join(dir, "tools-programmer")
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "../../cmd/tools-programmer")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(b), err)
		}
	}
	home := t.TempDir()
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, binary, "--db", ":memory:", "skill", "sync", "opencode")
		cmd.Dir = home // No source files, gh, credentials or database in this runtime fixture.
		cmd.Env = []string{"HOME=" + home, "PATH=" + home, "XDG_STATE_HOME=invalid-relative"}
		b, err := cmd.CombinedOutput()
		cancel()
		if err != nil || !json.Valid(b) {
			t.Fatal(string(b), err)
		}
		t.Log(string(b))
		assertSkill(t, home)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".config" {
		t.Fatal("unexpected standalone state", entries, err)
	}
}
