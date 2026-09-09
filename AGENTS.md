# tools-programmer

Independent Go CLI; no inherited application conventions. Artifacts are English. Use standard `database/sql`, `flag`, and the pinned pure-Go SQLite driver. Keep local review declarations, remote observations, durable mutation intents and confirmed approvals separate. Never bypass unknown intents or substitute CI for technical review.

## Checks

Run from this root:

```sh
gofmt -w cmd internal
go test ./...
go vet ./...
go test -race ./...
CGO_ENABLED=0 go build -trimpath -o bin/tools-programmer ./cmd/tools-programmer
TOOLS_PROGRAMMER_TEST_BINARY="$PWD/bin/tools-programmer" go test ./internal/cli -run TestBinarySmokeWithFakeGitHub -count=1
```

Tests must use temporary SQLite and fake GitHub/gh; never real credentials or remote writes. Do not create commits, remotes or globally install the skill unless explicitly requested. Keep generated database/build artifacts untracked. All GitHub writes belong exclusively to the exact-SHA approval adapter, preceded by durable atomic claim and followed by verified readback. Do not hold SQLite transactions over network I/O.

## Project skill registry

Before any explicitly authorized commit, load the project skill and run `tools-programmer project check --project NAME` from the intended checkout, using the registered project and existing database. Require success in the same environment as the commit; check both author and committer. Stop on unbound identity, checkout mismatch or identity mismatch. Never infer expected identities, switch GitHub accounts, change Git configuration, or create replacement state to bypass a block. Commit-time author overrides or preserved authors require separate verification; the ordinary pre-commit check does not certify those flows. Direct `git commit` bypasses this advisory gate; no hooks or global enforcement are installed.

| Skill | Trigger | Path |
|---|---|---|
| tools-programmer | Before commits, project identity check, PR review history, record review, approval plan, approve PR | `.opencode/skills/tools-programmer/SKILL.md` |

Read the skill before recording or applying PR reviews. Its local command reference travels with it. It is registered only in this project, not installed globally.
