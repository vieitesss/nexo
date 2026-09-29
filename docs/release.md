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

Tagged releases publish darwin and linux archives for amd64 and arm64, along
with checksums and a formula in `vieitesss/homebrew-tap`. Pull requests run a
GoReleaser snapshot build without publishing anything.
