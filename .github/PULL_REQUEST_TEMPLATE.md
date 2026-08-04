## Summary

One or two sentences. What does this PR change and why?

## Conventional commit

The squash commit becomes the release note, so the title and body
stand on their own. Use the `feat|fix|docs|chore` prefix.

## Checklist

- [ ] One issue per PR (per `CONTRIBUTING.md`).
- [ ] Branch name is `<type>/issue-<number>-<slug>`.
- [ ] `go test ./...` passes locally.
- [ ] `gofmt -l .` and `go vet ./...` are clean.
- [ ] New behaviour ships with a test in the same PR.
- [ ] If a tool was added or removed, `docs/tools.md` and
      `internal/mcp/tools_test.go` are in lock-step.
- [ ] If a flag / env-var was added, `docs/configuration.md` and
      the corresponding `internal/config` test are in lock-step.
- [ ] If this PR touches README content, README.es.md is updated
      in the same commit (per `CONTRIBUTING.md` translation policy).