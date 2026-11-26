package release

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/ericfaris/anycli-launcher/v4/internal/buildinfo"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(repoFile(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The pieces that must agree across .goreleaser.yaml, go.mod, buildinfo and
// the install scripts. goreleaser check (CI) validates the schema; this
// catches the cross-file drift it can't see.
func TestReleaseConfigAgrees(t *testing.T) {
	gr := read(t, ".goreleaser.yaml")

	mod := regexp.MustCompile(`(?m)^module (\S+)$`).FindStringSubmatch(read(t, "go.mod"))
	if mod == nil || mod[1] != buildinfo.ModulePath {
		t.Fatalf("go.mod module %v != buildinfo.ModulePath %s", mod, buildinfo.ModulePath)
	}
	// go install ...@latest only sees v4 tags with the /v4 suffix.
	if !strings.HasSuffix(buildinfo.ModulePath, "/v4") {
		t.Fatal("module path must end in /v4 for v4.x tags")
	}

	for _, want := range []string{
		"-X " + buildinfo.ModulePath + "/internal/buildinfo.version={{ .Version }}",
		"-X " + buildinfo.ModulePath + "/internal/buildinfo.commit={{ .FullCommit }}",
		"-X " + buildinfo.ModulePath + "/internal/buildinfo.date={{ .CommitDate }}",
		"CGO_ENABLED=0", "-trimpath", "main: ./cmd/ac", "binary: ac",
		"goos: [linux, darwin, windows]", "goarch: [amd64, arm64]",
		"project_name: anycli-launcher",
		// install.sh/install.ps1 and buildinfo.Install*URL rely on these.
		`name_template: "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}"`,
		"name_template: checksums.txt",
		"glob: ./install.sh", "glob: ./install.ps1",
		"name: homebrew-tap", "name: scoop-bucket",
		// Publishing to the tap/bucket is skipped without the token.
		`skip_upload: '{{ if index .Env "TAP_GITHUB_TOKEN" }}auto{{ else }}true{{ end }}'`,
		"- README.md", "- LICENSE", "- CHANGELOG.md",
	} {
		if !strings.Contains(gr, want) {
			t.Errorf(".goreleaser.yaml missing %q", want)
		}
	}
	if n := strings.Count(gr, `skip_upload: '{{ if index .Env "TAP_GITHUB_TOKEN" }}`); n != 2 {
		t.Errorf("skip_upload guard on %d publishers, want 2 (cask + scoop)", n)
	}

	sh := read(t, "install.sh")
	for _, want := range []string{`NAME="anycli-launcher"`, `asset="${NAME}_${os}_${arch}.tar.gz"`, `asset="${NAME}_${os}_${arch}.zip"`, "checksums.txt"} {
		if !strings.Contains(sh, want) {
			t.Errorf("install.sh missing %q", want)
		}
	}
	ps := read(t, "install.ps1")
	for _, want := range []string{`$asset = "anycli-launcher_windows_$arch.zip"`, "checksums.txt", "Get-Command ac -CommandType Application"} {
		if !strings.Contains(ps, want) {
			t.Errorf("install.ps1 missing %q", want)
		}
	}
	if !strings.HasPrefix(sh, "#!/bin/sh\n") {
		t.Error("install.sh must be POSIX sh")
	}
}

// The release workflow: tag-only trigger, least privilege, pinned actions.
func TestReleaseWorkflow(t *testing.T) {
	wf := read(t, ".github/workflows/release.yml")
	for _, want := range []string{
		"tags: [\"v*\"]",
		"contents: write", "id-token: write",
		"go test ./...",
		"TAP_GITHUB_TOKEN: ${{ secrets.TAP_GITHUB_TOKEN }}",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("release.yml missing %q", want)
		}
	}
	if regexp.MustCompile(`(?m)^\s*branches:`).MatchString(wf) {
		t.Error("release.yml must only run on tags")
	}
	// Every action is pinned to a vX.Y.Z tag (or a SHA), never a branch.
	for _, m := range regexp.MustCompile(`uses:\s*(\S+)`).FindAllStringSubmatch(wf, -1) {
		if !regexp.MustCompile(`@(v\d+\.\d+\.\d+|[0-9a-f]{40})$`).MatchString(m[1]) {
			t.Errorf("action not pinned to a version: %s", m[1])
		}
	}
	if regexp.MustCompile(`(?m)^\s*permissions:\s*write-all`).MatchString(wf) {
		t.Error("write-all permissions")
	}
}
