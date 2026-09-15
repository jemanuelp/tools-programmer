package core

import "testing"

func TestOriginAssociation(t *testing.T) {
	p := Project{Repo: "github.com/example/demo", OriginURL: "git@github-P.com:Example/Demo.git"}
	for _, tt := range []struct {
		raw  string
		want bool
	}{
		{"git@github-P.com:example/demo.git", true},
		{"ssh://git@github-p.com/example/demo", true},
		{"https://github.com/example/demo.git", true},
		{"git@github-X.com:example/demo.git", false},
		{"git@github-P.com:other/demo.git", false},
		{"git@github-P.com:example/other.git", false},
		{"https://token@github.com/example/demo", false},
		{"https://github.com/example/demo?", false},
		{"https://github.com/example/%64emo", false},
		{"git@github-P.com:example/demo.git\ngit@github-P.com:example/demo.git", false},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			if got := p.MatchesOrigin(tt.raw); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
	p.OriginURL = ""
	if p.MatchesOrigin("git@github-P.com:example/demo.git") {
		t.Fatal("unmapped alias accepted")
	}
	p.OriginURL = "git@github-P.com:other/demo.git"
	if p.MatchesOrigin(p.OriginURL) {
		t.Fatal("invalid stored association accepted")
	}
}

func TestOriginRejectsUnsafeInputs(t *testing.T) {
	for _, raw := range []string{"git@host:demo", "https://host/demo"} {
		if _, err := OriginRepo(raw); err == nil {
			t.Fatal("missing owner accepted")
		}
	}
	for _, raw := range []string{"", "example/demo", "https://token@host/example/demo", "ssh://git:secret@host/example/demo", "ssh://user@host/example/demo", "ssh://git@host:22/example/demo", "https://host/example/demo#", "https://host/example/demo?token=secret", "https://host/example/%64emo", "file:///example/demo", "ext::command", "git@host:example/demo\t", "git@host:example/demo\n", "https://host:/example/demo"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := OriginRepo(raw); err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
}
