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

## Branches

- `development` holds the work in progress. Pushing it publishes nothing.
- `main` is the stable branch. A push to `main` publishes a release when `VERSION` has no
  `v<VERSION>` tag yet (`.github/workflows/release.yml`): it runs the tests, builds the binaries
  and `SHA256SUMS`, tags the commit, and creates the GitHub Release with the `CHANGELOG.md`
  section as notes. A push to `main` whose version is already released does nothing.
- A version with `-beta.N` is published as a prerelease. The dashboard update card offers it only
  on the `beta` channel; the default `stable` channel offers only versions without a pre-release
  part.

## Steps

1. On `development`, move the entries under `## [Unreleased]` in `CHANGELOG.md` to a new
   `## [<version>] - <YYYY-MM-DD>` section and update the compare links at the bottom.
2. Write the new version to `VERSION`.
3. Run the checks:

   ```sh
   nix develop -c go vet ./...
   nix develop -c go test -count=1 ./...
   make web-check
   ```

4. Commit with `chore(release): <version>` and push `development`.
5. Merge `development` into `main` and push `main`. The release workflow tags and publishes it;
   dashboards on the matching channel show the update card within 6 hours, or at once with
   **Check now**.
6. Locally, `make build` gives the daily binary `./lana` and `make release` the cross-platform
   binaries in `dist/`. Do not run `make tag` for a release; the workflow creates the tag.

## Daily use

Restart the daemon after `make build` so it runs the new binary. Before an upgrade whose notes
mention a schema migration, copy `$LANA_HOME/lana.db` to keep a rollback point.
