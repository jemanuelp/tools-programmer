package core

import "testing"

func TestValidateIssueURL(t *testing.T) {
	base := "https://github.com/example/demo/issues/12"
	for _, value := range []string{"http://github.com/example/demo/issues/12", base + "?x=1", base + "?", base + "#", base + "#comment", base + "/", " " + base, "https://user@github.com/example/demo/issues/12", "https://github.com:443/example/demo/issues/12", "https://github.com/example/demo/pull/12", "https://github.com/example/demo/issues/012", "https://github.com/example/demo/issues/%31%32", "https://GITHUB.COM/example/demo/issues/12", "https://github.com/example/other/issues/12", "javascript:alert(1)"} {
		t.Run(value, func(t *testing.T) {
			if ValidateIssueURL("github.com/example/demo", 12, value) == nil {
				t.Fatal("accepted invalid URL")
			}
		})
	}
	for _, repo := range []string{"github.com/example/demo", "github.enterprise.example/owner/repo"} {
		if err := ValidateIssueURL(repo, 12, "https://"+repo+"/issues/12"); err != nil {
			t.Fatal(err)
		}
		if err := ValidateIssueURL(repo, 12, ""); err != nil {
			t.Fatal(err)
		}
	}
}
