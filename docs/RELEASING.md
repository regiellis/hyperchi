# Publishing HyperChi

The repository holds two Go modules, each tagged on its own:

| Module | Directory | Tag form |
| --- | --- | --- |
| `github.com/regiellis/hyperchi` (framework) | repository root | `vX.Y.Z` |
| `github.com/regiellis/hyperchi/cmd/hyperchi` (CLI) | `cmd/hyperchi` | `cmd/hyperchi/vX.Y.Z` |

The CLI lives in its own module so apps that import the framework never download cobra,
lipgloss, or the rest of the terminal stack. Users install it with:

```sh
go install github.com/regiellis/hyperchi/cmd/hyperchi@latest
```

`@latest` resolves the newest `cmd/hyperchi/v*` tag, so the CLI exists for `go install` only
after its first tag is pushed.

## Gate

From the framework checkout containing the reviewed changes:

```sh
task release:check
```

This builds, vets, and race-tests both modules (the CLI tests generate, compile, and
HTTP-test both project templates), runs `golangci-lint` in both, and scans both against
current vulnerability advisories using `govulncheck v1.8.0`. Run any optional module's tests
(`modules/*`) separately when that module is included in a release. Review existing frontend
work and run the frontend build before including it. Do not bypass a failing gate.

`scripts/prepare-release.sh v0.2.0` is a pre-flight after the gate, not a replacement for it:
it builds, vets, and tests both modules, confirms both module paths, checks that the framework
does not build CLI-only dependencies, warns when `cmd/hyperchi/go.mod` still has its replace
directive, and prints the tag commands below with the version filled in. It runs `go mod tidy`
in both modules, so review any `go.mod` change it leaves. It skips lint and the vulnerability
scan.

Before 1.0, a minor version (`v0.2.0`) may carry breaking changes, listed in `CHANGELOG.md`
under **BREAKING CHANGE**; a patch version (`v0.2.1`) does not.

## Tag sequence

The CLI module requires a framework version, and its `go.mod` carries
`replace github.com/regiellis/hyperchi => ../..` for local development. `go install
module@version` refuses a module whose `go.mod` contains a replace directive, so the tagged CLI
commit must not have one. Release in this order (shown for `v0.2.0`):

1. **Tag the framework.** With the release commit reviewed and the tree clean, create an
   annotated `v0.2.0` tag and push the commit and tag through the repository's normal
   publication process. CI runs for version tags. Confirm the version resolves from the proxy:
   `go list -m github.com/regiellis/hyperchi@v0.2.0`.
2. **Pin the CLI to the published framework.** In `cmd/hyperchi`:

   ```sh
   go mod edit -dropreplace=github.com/regiellis/hyperchi
   go get github.com/regiellis/hyperchi@v0.2.0
   go mod tidy
   go build ./... && go test -race -count=1 ./...
   ```

   Both `_scaffold/go.mod.tpl` and `_site/go.mod.tpl` must request the same framework
   version as the CLI's `go.mod`; `TestScaffoldsRequireTheCLIFrameworkVersion` fails when
   they drift. Commit this as the CLI release commit.
3. **Tag the CLI.** Create an annotated `cmd/hyperchi/v0.2.0` tag on that commit and push it.
   CI runs for `cmd/hyperchi/v*` tags too.
4. **Verify the install path.** Outside the checkout, run
   `go install github.com/regiellis/hyperchi/cmd/hyperchi@v0.2.0`, then `hyperchi new demo`,
   `cd demo && go mod tidy && CGO_ENABLED=0 go build .` with no `--framework-path`. That build
   must resolve everything from the proxy.
5. **Restore the development wiring.** Add the replace back
   (`go mod edit -replace=github.com/regiellis/hyperchi=../..`) in a follow-up commit, so local
   builds and CI test the CLI against the framework source beside it.

A framework-only patch release needs no CLI tag unless the scaffolds should request the new
version; in that case bump the templates and the CLI's `require` together and tag both.

Verify a framework tag resolves from the Go module proxy before switching consuming
applications to it.

## After the tags

pkg.go.dev lists a version once the module proxy has fetched it, which step 1's `go list -m`
request causes. The page (`https://pkg.go.dev/github.com/regiellis/hyperchi@v0.2.0`) can lag
by several minutes; visiting it requests indexing. If a version never appears, check that the
tag is on the remote (`git ls-remote --tags origin`) and that it is a full semver tag with the
`v` prefix.

A GitHub release is optional. Create it from the version's `CHANGELOG.md` section, for example
`gh release create v0.2.0 --title v0.2.0 --notes-file <notes>`. Like pushing a tag, it is
visible outside the repository. Until a version is on the proxy, `hyperchi new --framework-path` (or a CLI
built with `task cli`, which bakes in the checkout path) wires a local replace into generated
projects.

## Go version

The `go` directive in both modules is `1.26.0`, the build minimum. It cannot go lower: the
pinned `golang.org/x/crypto`, `golang.org/x/sys`, `golang.org/x/image`, `modernc.org/sqlite`,
and `modernc.org/libc` each declare `go 1.26.0`. Both modules build and test under
`GOTOOLCHAIN=go1.26.0` (checked 2026-10-05). There is no `toolchain` line.

The directive is not a security floor. Under Go 1.26.0, `govulncheck` reports reachable
standard library advisories fixed in 1.26.3 through 1.26.6 (net/http, crypto/tls,
html/template, and others). Build and deploy with the latest 1.26 patch release; CI scans
with it. Generated projects request `go 1.26.8`.

The tested dependency baseline is Go 1.26.8, chi 5.3.2, x/crypto 0.57.0, x/image 0.46.0, and
x/sys 0.48.0 (2026-10-03). These are the verified versions, not a claim that each is the
latest release. Go's current stable release was checked as 1.27.1 on 2026-10-03. Use current
advisory results, not this note, to assess release safety.
