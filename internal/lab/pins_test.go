package lab

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDependencyPinsAgreeWithBuildRecipe(t *testing.T) {
	b, err := os.ReadFile("../../lab/versions.json")
	if err != nil {
		t.Fatal(err)
	}
	var versions map[string]any
	if json.Unmarshal(b, &versions) != nil {
		t.Fatal("versions schema")
	}
	b, err = os.ReadFile("../../lab/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	recipe := string(b)
	for _, key := range []string{"ubuntu", "ubuntu_snapshot", "bootstrap_ca_url", "bootstrap_ca_sha256", "go_sha256", "node_sha256", "opencode_sha256"} {
		v, ok := versions[key].(string)
		if !ok || !strings.Contains(recipe, v) {
			t.Fatal("pin mismatch", key)
		}
	}
	b, err = os.ReadFile("../../lab/package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Packages map[string]struct{ Version, Resolved, Integrity string }
	}
	if json.Unmarshal(b, &lock) != nil {
		t.Fatal("lock schema")
	}
	for name, p := range lock.Packages {
		if name == "" {
			continue
		}
		if !strings.HasPrefix(p.Resolved, "https://registry.npmjs.org/") || !strings.HasPrefix(p.Integrity, "sha512-") {
			t.Fatal("unlocked dependency", name)
		}
	}
	if lock.Packages["node_modules/@opencode-ai/plugin"].Version != "1.18.30" || lock.Packages["node_modules/@opencode-ai/sdk"].Version != "1.18.30" {
		t.Fatal("plugin/SDK baseline mismatch")
	}
}
func TestMissingPluginPreflightDoesNotInstall(t *testing.T) {
	root := t.TempDir()
	if pluginReady(root) == nil {
		t.Fatal("missing dependencies accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("preflight mutated fixture", err)
	}
}

func TestVerifiedTrustBootstrapPrecedesHTTPSAPT(t *testing.T) {
	b, err := os.ReadFile("../../lab/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	recipe := string(b)
	bootstrap := strings.Index(recipe, "ADD --checksum=sha256:")
	apt := strings.Index(recipe, "apt-get")
	if bootstrap < 0 || apt < bootstrap {
		t.Fatal("APT runs before verified trust exists")
	}
	for _, required := range []string{"https://curl.se/ca/cacert-2026-08-13.pem", "https://snapshot.ubuntu.com/ubuntu/20260910T000000Z", "signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg", "Acquire::https::CaInfo=/etc/ssl/certs/ca-certificates.crt"} {
		if !strings.Contains(recipe, required) {
			t.Fatal("missing trust boundary", required)
		}
	}
	for _, forbidden := range []string{"http://snapshot", "Verify-Peer", "VerifyPeer", "--allow-unauthenticated", "trusted=yes", "curl -k"} {
		if strings.Contains(recipe, forbidden) {
			t.Fatal("verification weakened", forbidden)
		}
	}
}

func TestCATrustPathsHaveSeparateDirectoryAndFileModes(t *testing.T) {
	b, err := os.ReadFile("../../lab/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	recipe := string(b)
	add := strings.Index(recipe, "ADD --checksum=")
	dirs := strings.Index(recipe, "chmod 0755 /etc/ssl /etc/ssl/certs")
	apt := strings.Index(recipe, "apt-get")
	if add < 0 || dirs < add || apt < dirs {
		t.Fatal("CA directories are not made traversable before APT")
	}
	line := strings.SplitN(recipe[add:], "\n", 2)[0]
	if !strings.Contains(line, "--chmod=0644") {
		t.Fatal("CA file read mode is not explicit")
	}
}

func TestImageBuildUsesAbsoluteAdministratorExecutable(t *testing.T) {
	b, err := os.ReadFile("../../lab/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "/usr/sbin/useradd --uid 10001") {
		t.Fatal("useradd depends on a PATH that omits /usr/sbin")
	}
}
