# Contributing

This repository is archived and does not take pull requests. Fork it to
continue the work; the notes below are how the code was kept and still apply
to forks.

## Ground rules

- Do not submit copyrighted game executables, game assets, EA server binaries, database dumps, private keys, passwords, access tokens or other secrets.
- Keep machine-specific paths, hostnames, public IP addresses and personal server domains out of source and examples. Use documentation ranges such as `example.org` and `203.0.113.0/24` where an example is needed.
- Prefer protocol observations that can be reproduced. Document the client build, command/component, packet shape, offsets or other evidence needed to verify a finding.
- Keep generated files out of commits. The repository `.gitignore` covers the normal build/runtime outputs.

## Before opening a change

From `source\`:

```text
gofmt -w <changed .go files>
go test ./...
```

For Python tools, make sure they parse under Python 3. For Windows build/runtime changes, also exercise `Build_All.bat` and first-run setup when possible.

When changing behavior, update the matching document in `docs\`.
