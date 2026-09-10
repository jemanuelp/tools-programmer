package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func TestDispatchLookupNeverCreatesStateAndValidatesBeforeOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "db")
	for _, session := range []string{"parent", "bad id"} {
		var output bytes.Buffer
		if Run(context.Background(), []string{"--db", path, "activity", "dispatch-show", "--runtime", "r", "--session", session, "--call", "call"}, &output, forbiddenRunner{}) == 0 {
			t.Fatal("missing state accepted")
		}
		if session == "bad id" && !strings.Contains(output.String(), "invalid_input") {
			t.Fatal(output.String())
		}
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatal("lookup created directory")
		}
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, _ := os.ReadFile(path)
	var output bytes.Buffer
	if Run(context.Background(), []string{"--db", path, "activity", "dispatch-show", "--runtime", "r", "--session", "parent", "--call", "missing"}, &output, forbiddenRunner{}) != 0 {
		t.Fatal(output.String())
	}
	if !strings.Contains(output.String(), `"data": null`) {
		t.Fatal(output.String())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("lookup changed database")
	}
}

func TestBinaryDispatchTransportUsesSameDurableMarker(t *testing.T) {
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
		build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/tools-programmer")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %s %v", output, err)
		}
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "private", "history.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.AddProject(core.Project{Name: "demo", Repo: "example/demo", ExpectedGitHub: "reviewer", GitName: "Reviewer", GitEmail: "r@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	db.AddIssue("demo", 1, "")
	db.AddIssue("demo", 2, "")
	if err := db.RecordActivity(core.ActivityEvent{ID: "parent", Runtime: "r", Session: "p", Segment: "a", Kind: "bind", At: 100, Project: "demo", Issue: 1}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	module, _ := filepath.Abs("../../integrations/opencode/transport.mjs")
	moduleURL, _ := json.Marshal((&url.URL{Scheme: "file", Path: module}).String())
	binJSON, _ := json.Marshal(binary)
	dbJSON, _ := json.Marshal(path)
	script := `import {createRecorder,createDispatchLookup} from ` + string(moduleURL) + `;
import {createHash} from 'node:crypto';
const write=createRecorder(` + string(binJSON) + `,` + string(dbJSON) + `);
const marker='dispatch-'+createHash('sha256').update('r\0p\0call').digest('hex');
for(const kind of ['start','stop']) await write({id:marker+'-'+kind,runtime:'r',session:'p',segment:'a',kind,at:110,activity:marker,category:'delegation'});
await write({id:'switch',runtime:'r',session:'p',segment:'b',kind:'bind',at:120,project:'demo',issue:2});
const d=await createDispatchLookup(` + string(binJSON) + `,` + string(dbJSON) + `)('r','p','call');
await write({id:'child',runtime:'r',session:'c',segment:'c1',kind:'bind',at:130,parent_session:d.session,parent_segment:d.segment,dispatch_at:d.at});
console.log(JSON.stringify(d));`
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture: %s %v", output, err)
	}
	var d core.DispatchRecord
	if json.Unmarshal(output, &d) != nil || d.Segment != "a" || d.At != 110 {
		t.Fatal(string(output))
	}
	ro, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	_, segments, err := ro.IssueActivity("demo", 1)
	if err != nil || len(segments) != 2 {
		t.Fatal(segments, err)
	}
}
