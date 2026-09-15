package core

import (
	"net/url"
	"strings"
)

// OriginRepo parses a credential-free remote without resolving SSH aliases.
func OriginRepo(raw string) (string, error) {
	invalid := func() (string, error) {
		return "", Fail("invalid_input", "origin must be a credential-free HTTPS or git SSH repository URL without ports, escapes, query or fragment")
	}
	if raw == "" || len(raw) > 1024 || strings.ContainsAny(raw, "?#%\\") {
		return invalid()
	}
	for _, r := range raw {
		if r <= 32 || r == 127 {
			return invalid()
		}
	}
	var repo string
	if strings.HasPrefix(raw, "git@") {
		host, path, ok := strings.Cut(strings.TrimPrefix(raw, "git@"), ":")
		if !ok {
			return invalid()
		}
		repo = host + "/" + path
	} else {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "ssh") || u.Host == "" || strings.Contains(u.Host, ":") || u.Opaque != "" {
			return invalid()
		}
		if u.User != nil && (u.Scheme != "ssh" || u.User.String() != "git") {
			return invalid()
		}
		repo = u.Host + "/" + strings.TrimPrefix(u.Path, "/")
	}
	if len(strings.Split(repo, "/")) != 3 {
		return invalid()
	}
	parsed, err := CanonicalRepo(strings.TrimSuffix(repo, ".git"))
	if err != nil {
		return invalid()
	}
	return parsed, nil
}

func (p Project) ValidateOrigin(raw string) error {
	repo, err := OriginRepo(raw)
	if err != nil {
		return err
	}
	if strings.SplitN(repo, "/", 2)[1] != p.Slug() {
		return Fail("checkout_mismatch", "associated origin must retain the registered owner and repository")
	}
	return nil
}

func (p Project) MatchesOrigin(raw string) bool {
	repo, err := OriginRepo(raw)
	if err != nil {
		return false
	}
	if repo == p.Repo {
		return true
	}
	if p.ValidateOrigin(p.OriginURL) != nil {
		return false
	}
	associated, err := OriginRepo(p.OriginURL)
	return err == nil && repo == associated
}
