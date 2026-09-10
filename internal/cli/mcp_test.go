package cli

import (
	"bytes"
	"context"
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

func TestMCPNeverInitializesOrMigrates(t *testing.T) {
	for _, kind := range []string{"missing-parent", "missing-file", "v1", "future", "corrupt", "v2-eof"} {
		t.Run(kind, func(t *testing.T) {
			home := mcpHome(t)
			path := filepath.Join(home, "private", "history.db")
			if kind != "missing-parent" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "v1" || kind == "future" || kind == "v2-eof" {
				db, err := store.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				if kind != "v2-eof" {
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
			before, _ := os.ReadFile(path)
			var stdout, stderr bytes.Buffer
			code := RunWithIO(context.Background(), []string{"--db", path, "mcp", "serve"}, io.NopCloser(strings.NewReader("")), &stdout, &stderr, nil)
			if kind == "v2-eof" {
				if code != 0 || stderr.Len() != 0 {
					t.Fatalf("EOF failure: %d %s", code, &stderr)
				}
			} else if code == 0 || !strings.Contains(stderr.String(), "mcp_storage") {
				t.Fatalf("accepted invalid state: %d %s", code, &stderr)
			}
			if stdout.Len() != 0 || strings.Contains(stderr.String(), "SECRET_CORRUPT_DB") {
				t.Fatal("unsafe output", &stdout, &stderr)
			}
			after, _ := os.ReadFile(path)
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
	if err != nil || len(list.Tools) != 5 {
		t.Fatalf("%+v %v", list, err)
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
