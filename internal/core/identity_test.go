package core

import "testing"

func TestExpectedIdentity(t *testing.T) {
	for _, tt := range []struct {
		name, login, gitName, email string
		valid                       bool
	}{
		{"explicit", "reviewer", "Review User", "review@example.com", true},
		{"missing", "", "Review User", "review@example.com", false},
		{"name newline", "reviewer", "Review\nUser", "review@example.com", false},
		{"email delimiter", "reviewer", "Review User", "<review@example.com>", false},
		{"login whitespace", " reviewer", "Review User", "review@example.com", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := Project{ExpectedGitHub: tt.login, GitName: tt.gitName, GitEmail: tt.email}
			if (p.ValidateIdentity() == nil) != tt.valid {
				t.Fatal("unexpected identity validation")
			}
		})
	}
}
