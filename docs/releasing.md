# Releasing

lana uses [Semantic Versioning](https://semver.org/). Until the first stable release, every
version is a beta: `MAJOR.MINOR.PATCH-beta.N`, for example `0.1.0-beta.1`. The git tag is the
version with a `v` prefix: `v0.1.0-beta.1`.

The file `VERSION` holds the version that the next build carries. `make build` and
`make release` embed it, along with the short commit (with `-dirty` for uncommitted changes) and
the UTC build date. `lana --version` prints them:

```text
lana 0.1.0-beta.1 (ba69c93, 2026-11-02T01:00:00Z)
```

A plain `go build` without `make` reports `lana dev`.

## Which number to bump

| Change | Next version |
|---|---|
| Fixes only | `0.1.0-beta.2` (raise `N`) |
| New features, still in beta | `0.2.0-beta.1` (raise `MINOR`, reset `N`) |
| First stable release | `0.x.0` or `1.0.0` without `-beta` |

A change that needs action from the owner, such as a schema migration that old binaries refuse, a
renamed config key, or a removed tool, goes under "Upgrade notes" in `CHANGELOG.md`.

## Steps

1. Move the entries under `## [Unreleased]` in `CHANGELOG.md` to a new
   `## [<version>] - <YYYY-MM-DD>` section and update the compare links at the bottom.
2. Write the new version to `VERSION`.
3. Run the checks:

   ```sh
   nix develop -c go vet ./...
   nix develop -c go test -count=1 ./...
   make web-check
   ```

4. Commit with `chore(release): <version>`.
5. Run `make tag`. It refuses a dirty tree and an existing tag.
6. Run `make build` for the daily binary `./lana`. Run `make release` for the cross-platform
   binaries in `dist/`.
7. Push when you want the tag on GitHub: `git push origin development --follow-tags`.

## Daily use

Restart the daemon after `make build` so it runs the new binary. Before an upgrade whose notes
mention a schema migration, copy `$LANA_HOME/lana.db` to keep a rollback point.
