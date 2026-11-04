package cli

import (
	"context"
	"errors"
	"testing"

	"tools-programmer/internal/core"
)

func TestBoundedHostEngine(t *testing.T) {
	for _, tc := range []struct{ name, script, target, code string }{
		{"matching V1", "echo 1.18.30", "opencode", ""},
		{"matching V2 candidate", "echo 2.2.5", "opencode-v2", ""},
		{"wrong target", "echo 2.2.5", "opencode", "host_version"},
		{"unparseable", "echo private-output", "opencode", "host_probe"},
		{"bounded output", "while :; do printf '1234567890'; done", "opencode", "host_probe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := fakeSyncHost(t, tc.script)
			err := checkSyncHost(context.Background(), tc.target, host)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var typed *core.Error
			if !errors.As(err, &typed) || typed.Code != tc.code {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
		})
	}
}
