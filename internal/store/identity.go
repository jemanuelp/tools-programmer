package store

import "tools-programmer/internal/core"

// BindProject sets only previously absent identity policy. History and plans remain immutable.
func (s *Store) BindProject(name, repo, login, gitName, email string) (core.Project, error) {
	p := core.Project{ExpectedGitHub: login, GitName: gitName, GitEmail: email}
	if err := p.ValidateIdentity(); err != nil {
		return p, err
	}
	canonical, err := core.CanonicalRepo(repo)
	if err != nil {
		return p, err
	}
	r, err := s.DB.Exec(`UPDATE projects SET expected_github=?,git_name=?,git_email=?
WHERE name=? AND repo=? AND expected_github='' AND git_name='' AND git_email=''
AND NOT EXISTS(SELECT 1 FROM operations WHERE project=? AND state!='confirmed' AND lower(actor)!=lower(?))`, login, gitName, email, name, canonical, name, login)
	if err != nil {
		return p, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return p, err
	}
	if n != 1 {
		return p, core.Fail("identity_binding_blocked", "binding requires an existing unbound project, matching repository, and an expected login matching every unresolved intent actor; existing bindings cannot be replaced")
	}
	return s.Project(name)
}
