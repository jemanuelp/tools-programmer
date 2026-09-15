package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tools-programmer/internal/store"
)

func TestUnavailableIntegrationWarnsWithoutCreatingState(t *testing.T) {
	for _, command := range [][]string{
		{"activity", "context", "--runtime", "r", "--session", "native", "--directory", t.TempDir()},
		{"activity", "record", "--existing-only"},
	} {
		for _, existingDirectory := range []bool{false, true} {
			path := filepath.Join(t.TempDir(), "private", "history.db")
			if existingDirectory {
				if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			input := io.NopCloser(strings.NewReader(`{"id":"a","runtime":"r","session":"native","segment":"a","kind":"bind","at":1}`))
			if RunWithIO(context.Background(), append([]string{"--db", path}, command...), input, &out, &out, forbiddenRunner{}) == 0 {
				t.Fatal("missing state accepted")
			}
			if strings.Contains(out.String(), path) {
				t.Fatal("private path leaked")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("state created", err)
			}
		}
	}
}

func TestActivityContextDoesNotModifyExistingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "history.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if Run(context.Background(), []string{"--db", path, "activity", "context", "--runtime", "r", "--session", "native", "--directory", t.TempDir()}, &out, forbiddenRunner{}) != 0 {
		t.Fatal(out.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read changed database", err)
	}
	if !strings.Contains(out.String(), `"assignment": "unknown"`) {
		t.Fatal(out.String())
	}
}
