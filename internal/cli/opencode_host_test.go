package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseOpenCodeVersionRequiresOneVersionLine(t *testing.T) {
	for _, tt := range []struct {
		name    string
		output  string
		version string
		ok      bool
	}{
		{name: "release --version output", output: "1.18.30\n", version: "1.18.30", ok: true},
		{name: "unverified label", output: "opencode 1.18.30\n"},
		{name: "unverified v prefix", output: "v1.18.30\n"},
		{name: "wrong capitalization", output: "OpenCode 1.18.30\n"},
		{name: "extra text", output: "1.18.30 release\n"},
		{name: "multiple lines", output: "1.18.30\nextra\n"},
		{name: "leading zero", output: "01.18.30\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseOpenCodeVersion([]byte(tt.output))
			if ok != tt.ok || got != tt.version {
				t.Fatalf("parseOpenCodeVersion(%q) = %q, %v; want %q, %v", tt.output, got, ok, tt.version, tt.ok)
			}
		})
	}
}

func TestSelectOpenCodeBinaryUsesFirstPATHMatch(t *testing.T) {
	first := writeFakeOpenCode(t, "printf '1.18.30\\n'\n")
	second := writeFakeOpenCode(t, "printf '1.18.30\\n'\n")
	t.Setenv("PATH", filepath.Dir(first)+string(os.PathListSeparator)+filepath.Dir(second))

	got, err := selectOpenCodeBinary("")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(first)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("selected %q, want first PATH match %q", got, want)
	}
}

func TestSelectOpenCodeBinaryHonorsExplicitPath(t *testing.T) {
	fake := writeFakeOpenCode(t, "printf '1.18.30\\n'\n")
	t.Setenv("PATH", t.TempDir())
	got, err := selectOpenCodeBinary(fake)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(fake)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("selected %q, want explicit path %q", got, want)
	}
}

func TestProbeOpenCodeVersionTimesOut(t *testing.T) {
	fake := writeFakeOpenCode(t, "while :; do :; done\n")
	_, err := probeOpenCodeVersion(fake, 25*time.Millisecond)
	if !errors.Is(err, errOpenCodeProbeTimeout) {
		t.Fatalf("probe error = %v, want timeout", err)
	}
}

func TestProbeOpenCodeVersionBoundsCombinedOutput(t *testing.T) {
	fake := writeFakeOpenCode(t, "printf '%700s' ''\nprintf '%700s' '' >&2\n")
	_, err := probeOpenCodeVersion(fake, time.Second)
	if !errors.Is(err, errOpenCodeOutputLarge) {
		t.Fatalf("probe error = %v, want output limit error", err)
	}
}

func TestProbeOpenCodeVersionCancelsContinuousInheritedOutput(t *testing.T) {
	stop, exited := filepath.Join(t.TempDir(), "stop"), filepath.Join(t.TempDir(), "exited")
	t.Setenv("OPENCODE_TEST_STOP", stop)
	t.Setenv("OPENCODE_TEST_EXITED", exited)
	fake := writeFakeOpenCode(t, `(trap '' PIPE; while [ ! -e "$OPENCODE_TEST_STOP" ]; do printf x || :; done; : > "$OPENCODE_TEST_EXITED") &`+"\nexit 0\n")
	guard := time.AfterFunc(800*time.Millisecond, func() { _ = os.WriteFile(stop, nil, 0600) })
	t.Cleanup(func() {
		defer guard.Stop()
		if err := os.WriteFile(stop, nil, 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for {
			if _, err := os.Stat(exited); err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("continuous-output child did not exit")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})

	started := time.Now()
	_, err := probeOpenCodeVersion(fake, 3*time.Second)
	if !errors.Is(err, errOpenCodeOutputLarge) {
		t.Fatalf("probe error = %v, want output limit error", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("continuous-output probe took %v; want cancellation and bounded pipe wait", elapsed)
	}
}
