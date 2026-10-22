# Release and distribution

## Install from source

```sh
go install github.com/vieitesss/nexo/cmd/nexo@latest
```

## Install with Homebrew

```sh
brew install vieitesss/tap/nexo
```

Check the installed build with `nexo --version`.

`nexo upgrade` installs the latest release on this machine and configured
remotes; `nexo update` is an alias. Use `nexo upgrade --nightly` for the `main`
branch, or `--version vX.Y.Z` for a specific tag. Upgrade builds the requested
revision on the machine running the command, so it needs a Go toolchain and Git
there; a Homebrew-owned nexo is left to `brew upgrade vieitesss/tap/nexo`.

Tagged releases publish darwin and linux archives for amd64 and arm64, along
with checksums and a formula in `vieitesss/homebrew-tap`. Pull requests run a
GoReleaser snapshot build without publishing anything.
