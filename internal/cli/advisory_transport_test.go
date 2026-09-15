package cli

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

// This proves the subprocess/SQLite boundary, not native host hook activation or
// model interpretation of an implementation request.
func TestBinaryAdvisoryPersistedTimeoutRequiresReadback(t *testing.T) {
	if testing.Short() {
		t.Skip("isolated binary and Node fixture")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	binary := os.Getenv("TOOLS_PROGRAMMER_TEST_BINARY")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "tools-programmer")
		cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/tools-programmer")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, out)
		}
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "private", "history.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.AddProject(core.Project{Name: "demo", Repo: "example/demo", Path: dir, ExpectedGitHub: "reviewer", GitName: "Reviewer", GitEmail: "fixture@example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := db.AddIssueWithURL("demo", 19, "", "https://github.com/example/demo/issues/19")
	if err != nil {
		t.Fatal(err)
	}
	module := func(name string) string {
		t.Helper()
		path, err := filepath.Abs("../../integrations/opencode/" + name)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal((&url.URL{Scheme: "file", Path: path}).String())
		return string(b)
	}
	values, _ := json.Marshal([]string{binary, path, dir})
	script := `import assert from 'node:assert/strict';
import {createRecorder} from ` + module("transport.mjs") + `;
import {createContextLookup} from ` + module("context.mjs") + `;
import {createObserver} from ` + module("observer.mjs") + `;
const [binary,database,directory]=` + string(values) + `;
const lookup=createContextLookup(binary,database,directory);
const write=createRecorder(binary,database);
let fail=true;
const observer=createObserver({runtime:'r',readContext:lookup,record:async e=>{
  await write(e);
  if(e.kind==='bind' && fail){fail=false;throw new Error('persisted timeout');}
}});
assert.equal((await lookup('r','native')).assignment,'unknown');
await assert.rejects(observer.assign({sessionID:'native'},'demo',19));
const saved=await lookup('r','native');
assert.equal(saved.issue.url,'https://github.com/example/demo/issues/19');
assert.equal(saved.assignment,'assigned');
await Promise.all(Array.from({length:6},()=>observer.assign({sessionID:'native'},'demo',19)));
assert.equal((await lookup('r','native')).segment,saved.segment);
assert.equal((await lookup('other-origin','native')).assignment,'unknown');
assert.equal((await lookup('r','other-session')).assignment,'unknown');
await observer.dispose();`
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	var segments, outcomes int
	if err := db.DB.QueryRow("SELECT count(*) FROM activity_segments WHERE issue_id=?", issue.ID).Scan(&segments); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.QueryRow("SELECT count(*) FROM issue_outcomes").Scan(&outcomes); err != nil {
		t.Fatal(err)
	}
	if segments != 1 || outcomes != 0 {
		t.Fatal("unexpected persisted effects", segments, outcomes)
	}
}
