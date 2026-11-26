// Package release holds tests for the release tooling at the repo root:
// install.sh, install.ps1 and .goreleaser.yaml. It has no code of its own.
// The install tests serve a fake release from a local httptest server, so
// they never touch the network.
package release
