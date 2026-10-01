package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func mcpHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("TOOLS_PROGRAMMER_DB", "")
	return home
}

func TestMCPValidationBeforeSideEffects(t *testing.T) {
	for _, tt := range []struct {
		args    []string
		success bool
	}{
		{[]string{"mcp", "serve", "--help"}, true},
		{[]string{"mcp", "install", "--help"}, true},
		{[]string{"mcp", "serve", "--unknown"}, false},
		{[]string{"mcp", "serve", "extra"}, false},
		{[]string{"mcp", "serve", "--enable-project-registration", "extra"}, false},
		{[]string{"mcp", "serve", "--enable-project-registration=invalid"}, false},
		{[]string{"mcp", "install"}, false},
		{[]string{"mcp", "install", "future-agent"}, false},
		{[]string{"mcp", "install", "opencode", "extra"}, false},
		{[]string{"mcp", "unknown"}, false},
	} {
		t.Run(strings.Join(tt.args, "-"), func(t *testing.T) {
			home := mcpHome(t)
			var stdout, stderr bytes.Buffer
			code := RunWithIO(context.Background(), tt.args, io.NopCloser(strings.NewReader("")), &stdout, &stderr, nil)
			if (code == 0) != tt.success {
				t.Fatalf("%d %s %s", code, &stdout, &stderr)
			}
			files, _ := os.ReadDir(home)
			if len(files) != 0 {
				t.Fatalf("validation created files: %v", files)
			}
			if tt.args[1] == "serve" && !tt.success && stdout.Len() != 0 {
				t.Fatal("protocol stdout contaminated", &stdout)
			}
		})
	}
}

// Build the historical schema directly: opening through store.Open would migrate it.
func mcpV2Fixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE projects(name TEXT PRIMARY KEY, repo TEXT NOT NULL UNIQUE, path TEXT NOT NULL, allow_no_checks INTEGER NOT NULL CHECK(allow_no_checks IN (0,1)), created_at TEXT NOT NULL);
CREATE TABLE snapshots(project TEXT NOT NULL REFERENCES projects(name), pr INTEGER NOT NULL CHECK(pr>0), payload TEXT NOT NULL, PRIMARY KEY(project,pr));
CREATE TABLE local_reviews(id INTEGER PRIMARY KEY AUTOINCREMENT, project TEXT NOT NULL REFERENCES projects(name), pr INTEGER NOT NULL CHECK(pr>0), sha TEXT NOT NULL, reviewer TEXT NOT NULL, method TEXT NOT NULL, outcome TEXT NOT NULL CHECK(outcome IN ('approved','changes_requested','incomplete')), summary TEXT NOT NULL, evidence TEXT NOT NULL, recorded_at TEXT NOT NULL);
CREATE INDEX review_head ON local_reviews(project,pr,sha,id);
CREATE TABLE plans(id TEXT PRIMARY KEY, project TEXT NOT NULL REFERENCES projects(name), payload TEXT NOT NULL);
CREATE TABLE operations(id TEXT PRIMARY KEY, plan_id TEXT NOT NULL REFERENCES plans(id), project TEXT NOT NULL REFERENCES projects(name), pr INTEGER NOT NULL, sha TEXT NOT NULL, actor TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('intent','unknown','confirmed')), review_id INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(project,pr,sha,actor));
CREATE TABLE confirmations(operation_id TEXT PRIMARY KEY REFERENCES operations(id), payload TEXT NOT NULL);
ALTER TABLE projects ADD COLUMN expected_github TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN git_name TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN git_email TEXT NOT NULL DEFAULT '';
PRAGMA user_version=2;
INSERT INTO projects VALUES('legacy','example/legacy','',0,'2026-01-01','reviewer','Reviewer','reviewer@example.com');`)
	if err != nil {
		t.Fatal(err)
	}
	var version, activityTables int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatal("fixture is not v2", version, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('issues','activity_events','activity_segments')").Scan(&activityTables); err != nil || activityTables != 0 {
		t.Fatal("fixture contains v3 tables", activityTables, err)
	}
}

func TestMCPNeverInitializesOrMigrates(t *testing.T) {
	for _, kind := range []string{"missing-parent", "missing-file", "v1", "future", "corrupt", "v2", "current-eof"} {
		t.Run(kind, func(t *testing.T) {
			home := mcpHome(t)
			path := filepath.Join(home, "private", "history.db")
			if kind != "missing-parent" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "v2" {
				mcpV2Fixture(t, path)
			}
			if kind == "v1" || kind == "future" || kind == "current-eof" {
				db, err := store.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				if kind != "current-eof" {
					version := "1"
					if kind == "future" {
						version = "99"
					}
					if _, err = db.DB.Exec("PRAGMA user_version=" + version); err != nil {
						t.Fatal(err)
					}
				}
				db.Close()
			}
			if kind == "corrupt" {
				if err := os.WriteFile(path, []byte("SECRET_CORRUPT_DB"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, readErr := os.ReadFile(path)
			if kind != "missing-parent" && kind != "missing-file" && readErr != nil {
				t.Fatal(readErr)
			}
			var stdout, stderr bytes.Buffer
			code := RunWithIO(context.Background(), []string{"--db", path, "mcp", "serve", "--enable-project-registration"}, io.NopCloser(strings.NewReader("")), &stdout, &stderr, nil)
			if kind == "current-eof" {
				if code != 0 || stderr.Len() != 0 {
					t.Fatalf("EOF failure: %d %s", code, &stderr)
				}
			} else if code == 0 || !strings.Contains(stderr.String(), "mcp_storage") {
				t.Fatalf("accepted invalid state: %d %s", code, &stderr)
			}
			if stdout.Len() != 0 || strings.Contains(stderr.String(), "SECRET_CORRUPT_DB") {
				t.Fatal("unsafe output", &stdout, &stderr)
			}
			after, readErr := os.ReadFile(path)
			if before != nil && readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("state modified")
			}
			if before == nil {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("database created")
				}
			}
			if kind == "missing-parent" {
				if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
					t.Fatal("directory created")
				}
			}
		})
	}
}

func TestMCPInstallPinsSelectedDatabaseWithoutOpeningIt(t *testing.T) {
	for _, source := range []string{"explicit", "environment", "default"} {
		t.Run(source, func(t *testing.T) {
			home := mcpHome(t)
			t.Chdir(home)
			args := []string{"mcp", "install", "opencode"}
			want := filepath.Join(home, "state", "tools-programmer", "history.db")
			if source == "environment" {
				t.Setenv("TOOLS_PROGRAMMER_DB", "env/history.db")
				want = filepath.Join(home, "env", "history.db")
			}
			if source == "explicit" {
				t.Setenv("TOOLS_PROGRAMMER_DB", "ignored/history.db")
				args = append([]string{"--db", "explicit/history.db"}, args...)
				want = filepath.Join(home, "explicit", "history.db")
			}
			var out, errOut bytes.Buffer
			if code := RunWithIO(context.Background(), args, io.NopCloser(strings.NewReader("")), &out, &errOut, nil); code != 0 {
				t.Fatalf("%s %s", &out, &errOut)
			}
			var result struct {
				Data struct {
					Command []string `json:"command"`
				} `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Data.Command) != 5 || result.Data.Command[2] != want {
				t.Fatalf("%s", &out)
			}
			if _, err := os.Stat(filepath.Dir(want)); !os.IsNotExist(err) {
				t.Fatal("installer opened database")
			}
		})
	}
}

func TestBinaryMCPInstalledCommandAndLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("binary MCP subprocess integration")
	}
	dir := t.TempDir()
	binary := os.Getenv("TOOLS_PROGRAMMER_TEST_BINARY")
	if binary == "" {
		binary = filepath.Join(dir, "tools-programmer")
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/tools-programmer")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(b), err)
		}
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "private", "history.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.AddProject(core.Project{Name: "binary-demo", Repo: "example/binary", ExpectedGitHub: "reviewer", GitName: "Reviewer", GitEmail: "reviewer@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, _ := os.ReadFile(path)
	env := []string{"HOME=" + dir, "PATH=" + filepath.Join(dir, "no-executables")}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	install := exec.CommandContext(ctx, binary, "--db", "private/history.db", "mcp", "install", "opencode")
	install.Env = env
	install.Dir = dir
	if b, err := install.CombinedOutput(); err != nil {
		t.Fatal(string(b), err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".config", "opencode", "opencode.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCP map[string]struct {
			Command []string `json:"command"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	command := cfg.MCP["tools-programmer"].Command
	if len(command) != 5 || command[2] != path {
		t.Fatal(command)
	}
	other := t.TempDir()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = other
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "binary-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.InitializeResult().ProtocolVersion == "" {
		t.Fatal("no initialization")
	}
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 11 {
		t.Fatalf("%+v %v", list, err)
	}
	registration := map[string]any{"name": "opt-in", "repo": "Example/Opt-In", "github_login": "Explicit", "git_name": "Explicit", "git_email": "explicit@example.com"}
	rejected, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "project_register", Arguments: registration})
	if err == nil && !rejected.IsError {
		t.Fatal("default binary accepted project registration")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "project_list"})
	if err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var got struct {
		Data []core.Project `json:"data"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil || !reflect.DeepEqual(got.Data, []core.Project{p}) {
		t.Fatalf("%s %v", encoded, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatal("unexpected diagnostics", &stderr)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		t.Fatalf("unclean EOF exit: %v", cmd.ProcessState)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("binary modified history")
	}

	// The same entry point permits only explicit local registration, without gh on PATH.
	writeCmd := exec.CommandContext(ctx, command[0], command[1:]...)
	writeCmd.Dir, writeCmd.Env = other, env
	writer, err := client.Connect(ctx, &mcp.CommandTransport{Command: writeCmd, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	issueURL := "https://github.com/example/binary/issues/12"
	result, err = writer.CallTool(ctx, &mcp.CallToolParams{Name: "issue_register", Arguments: map[string]any{"project": p.Name, "number": 12, "url": issueURL}})
	if err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	result, err = writer.CallTool(ctx, &mcp.CallToolParams{Name: "issue_summary", Arguments: map[string]any{"project": p.Name, "number": 12}})
	if err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	encoded, _ = json.Marshal(result.StructuredContent)
	if !strings.Contains(string(encoded), issueURL) {
		t.Fatalf("missing stored URL: %s", encoded)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	// The opt-in binary declares projects with no Git/gh executables or credentials.
	optArgs := append(append([]string{}, command[1:]...), "--enable-project-registration")
	optCmd := exec.CommandContext(ctx, command[0], optArgs...)
	optCmd.Dir, optCmd.Env = other, env
	optSession, err := client.Connect(ctx, &mcp.CommandTransport{Command: optCmd, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer optSession.Close()
	result, err = optSession.CallTool(ctx, &mcp.CallToolParams{Name: "project_register", Arguments: registration})
	if err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	registered, _ := json.Marshal(result.StructuredContent)
	var declared struct {
		Data core.Project `json:"data"`
	}
	if err := json.Unmarshal(registered, &declared); err != nil {
		t.Fatal(err)
	}
	result, err = optSession.CallTool(ctx, &mcp.CallToolParams{Name: "project_list"})
	if err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	listed, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(listed), "github.com/example/opt-in") {
		t.Fatalf("MCP readback missing declaration: %s", listed)
	}
	optSession.Close()
	cliList := exec.CommandContext(ctx, binary, "--db", path, "project", "list")
	cliList.Dir, cliList.Env = other, env
	listed, err = cliList.Output()
	var cliProjects struct {
		Data []core.Project `json:"data"`
	}
	if err != nil || json.Unmarshal(listed, &cliProjects) != nil || len(cliProjects.Data) != 2 || cliProjects.Data[1] != declared.Data {
		t.Fatalf("CLI readback mismatch: %s %v", listed, err)
	}

	// Startup failure uses stderr only and must not create a missing database.
	missing := filepath.Join(other, "missing", "history.db")
	bad := exec.CommandContext(ctx, binary, "--db", missing, "mcp", "serve")
	bad.Env = env
	bad.Dir = other
	var stdout, errors bytes.Buffer
	bad.Stdout = &stdout
	bad.Stderr = &errors
	if err := bad.Run(); err == nil || stdout.Len() != 0 || !strings.Contains(errors.String(), "mcp_storage") {
		t.Fatalf("%v %s %s", err, &stdout, &errors)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatal("missing database parent created")
	}

	// Signal cancellation while stdin remains open must stop the actual binary.
	signalCmd := exec.CommandContext(ctx, command[0], command[1:]...)
	signalCmd.Env = env
	signalCmd.Dir = other
	pipe, err := signalCmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	protocol, err := signalCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := signalCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { signalCmd.Process.Kill() })
	// Exchange initialize first, so the signal tests the running server rather
	// than racing process startup before signal.NotifyContext is installed.
	if _, err := io.WriteString(pipe, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-11-25\",\"capabilities\":{},\"clientInfo\":{\"name\":\"signal-test\",\"version\":\"1\"}}}\n"); err != nil {
		t.Fatal(err)
	}
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(protocol).Decode(&response); err != nil || response.JSONRPC != "2.0" || response.ID != 1 || len(response.Result) == 0 {
		t.Fatalf("non-protocol stdout: %+v %v", response, err)
	}
	if err := signalCmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- signalCmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("signal shutdown", err)
		}
	case <-time.After(3 * time.Second):
		signalCmd.Process.Kill()
		t.Fatal("signal shutdown timed out")
	}
}
