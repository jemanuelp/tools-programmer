package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tools-programmer/internal/core"
)

func TestOpenCodeV2PreflightUsesExactReleasedOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", home)
	t.Setenv("OPENCODE_SSH_ASKPASS_PORT", "must-not-inherit")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "must-not-inherit")
	t.Setenv("NODE_OPTIONS", "must-not-inherit")
	cases := []struct {
		name, body, code string
	}{
		{"released output", `printf 'opencode v2.0.26\n'`, ""},
		{"crlf", `printf 'opencode v2.0.26\r\n'`, ""},
		{"raw version rejected", `printf '2.0.26\n'`, "opencode_version_invalid"},
		{"old candidate rejected", `printf 'opencode v2.0.23\n'`, "opencode_version_mismatch"},
		{"wrong V2", `printf 'opencode v2.0.24\n'`, "opencode_version_mismatch"},
		{"V1 rejected", `printf '1.18.30\n'`, "opencode_version_invalid"},
		{"extra stderr", `printf 'opencode v2.0.26\n'; printf 'private error' >&2`, "opencode_version_invalid"},
		{"failure", `printf 'private error' >&2; exit 1`, "opencode_probe_failed"},
		{"overflow", `printf '%01025d' 0`, "opencode_version_output_too_large"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "fake-opencode")
			script := "#!/bin/sh\nset -eu\ntest \"$#\" = 1 && test \"$1\" = --version\n" +
				"test -z \"${OPENCODE_SSH_ASKPASS_PORT-}${OTEL_EXPORTER_OTLP_ENDPOINT-}${NODE_OPTIONS-}\"\n" + tt.body + "\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			selected, err := preflightOpenCodeV2(binary)
			if tt.code == "" {
				if err != nil || selected != binary {
					t.Fatal(selected, err)
				}
			} else {
				var typed *core.Error
				if selected != "" || !errors.As(err, &typed) || typed.Code != tt.code || strings.Contains(err.Error(), "private error") {
					t.Fatal("unexpected refusal", selected, err)
				}
			}
		})
	}
	if _, err := preflightOpenCodeV2(filepath.Join(home, "absent")); err == nil {
		t.Fatal("missing explicit executable accepted")
	}
	if _, err := preflightOpenCodeV2(""); err == nil {
		t.Fatal("missing PATH executable accepted")
	}
}

func TestOpenCodeV2PreflightIsolatesStartupWrites(t *testing.T) {
	operator := t.TempDir()
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR"} {
		t.Setenv(key, operator)
	}
	record := filepath.Join(t.TempDir(), "scratch-path")
	body := "#!/bin/sh\nset -eu\n" +
		"test \"$PWD\" = \"$HOME\"\n" +
		"test \"$HOME\" != '" + operator + "'\n" +
		"for root in \"$HOME\" \"$XDG_CONFIG_HOME\" \"$XDG_DATA_HOME\" \"$XDG_CACHE_HOME\" \"$XDG_STATE_HOME\" \"$XDG_RUNTIME_DIR\" \"$TMPDIR\"; do\n" +
		"case \"$root\" in \"$HOME\"|\"$HOME\"/*) ;; *) exit 2 ;; esac\n" +
		"printf startup > \"$root/startup-log\"\ndone\n" +
		"printf '%s' \"$HOME\" > '" + record + "'\n" +
		"printf 'opencode v2.0.26\\n'\n"
	binary := filepath.Join(t.TempDir(), "fake-opencode")
	if err := os.WriteFile(binary, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := preflightOpenCodeV2(binary); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(operator)
	if err != nil || len(entries) != 0 {
		t.Fatal("operator state changed", entries, err)
	}
	scratch, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(scratch)); !os.IsNotExist(err) {
		t.Fatal("probe scratch not removed", err)
	}
}

func TestOpenCodeV2VersionParserAndDeadline(t *testing.T) {
	for _, text := range []string{"opencode2 v2.0.23\n", "opencode 2.0.23\n", " opencode v2.0.23\n", "opencode v2.0.23\n\n", "\x1b[1mopencode\x1b[0m v2.0.23\n", "opencode v02.0.23\n"} {
		if _, ok := parseOpenCodeV2Version([]byte(text)); ok {
			t.Fatalf("unverified version shape accepted: %q", text)
		}
	}
	binary := filepath.Join(t.TempDir(), "sleeping-fake")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec /bin/sleep 5\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := probeOpenCodeVersionWith(binary, 20*time.Millisecond, []string{"PATH=/usr/bin:/bin"}, parseOpenCodeV2Version)
	if !errors.Is(err, errOpenCodeProbeTimeout) {
		t.Fatal("fake deadline was not enforced", err)
	}
}
