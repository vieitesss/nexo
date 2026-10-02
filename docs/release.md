# Release and distribution

These installation instructions apply once this repository is public and the
first `v*` tag has been released. Until then, use a local checkout to build
nexo.

## Install from source

```sh
go install github.com/vieitesss/nexo/cmd/nexo@latest
```

## Install with Homebrew

```sh
brew install vieitesss/tap/nexo
```

Check the installed build with `nexo --version`.

Once a tagged release exists, `nexo upgrade` installs that latest release on
this machine and configured remotes; `nexo update` is an alias. Use
`nexo upgrade --nightly` for the authenticated `main` branch, or
`--version vX.Y.Z` for a specific tag. Until the first release is published,
the default latest-release request fails and points to `--nightly`.

Tagged releases publish darwin and linux archives for amd64 and arm64, along
with checksums and a formula in `vieitesss/homebrew-tap`. Pull requests run a
GoReleaser snapshot build without publishing anything.
