package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeBinary is what the fixture archives contain as ac: a script that
// identifies itself, so the installed file can be checked by content.
const fakeBinary = "#!/bin/sh\necho 'anycli-launcher 9.9.9 (fixture)'\n"

var platforms = []struct{ os, arch string }{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

func assetName(goos, arch string) string {
	if goos == "windows" {
		return fmt.Sprintf("anycli-launcher_%s_%s.zip", goos, arch)
	}
	return fmt.Sprintf("anycli-launcher_%s_%s.tar.gz", goos, arch)
}

func tarGz(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ n, b string }{{"README.md", "readme\n"}, {"LICENSE", "MIT\n"}, {name, body}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.n, Mode: 0o755, Size: int64(len(f.b))}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(f.b))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipped(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct{ n, b string }{{"README.md", "readme\n"}, {name, body}} {
		w, err := zw.Create(f.n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(f.b))
	}
	zw.Close()
	return buf.Bytes()
}

// release is a fake GitHub release served over local HTTP at
// <URL>/latest/download/<asset> and <URL>/download/<tag>/<asset>.
type release struct {
	URL   string
	files map[string][]byte // asset name -> content
	mu    sync.Mutex
	paths []string
}

func (r *release) requested() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

// newRelease builds every platform's archive plus checksums.txt. mutate may
// edit the files (e.g. tamper with an archive) before checksums are kept as
// computed from the originals.
func newRelease(t *testing.T, mutate func(files map[string][]byte, sums map[string]string)) *release {
	t.Helper()
	r := &release{files: map[string][]byte{}}
	sums := map[string]string{}
	for _, p := range platforms {
		name := assetName(p.os, p.arch)
		var b []byte
		if p.os == "windows" {
			b = zipped(t, "ac.exe", fakeBinary)
		} else {
			b = tarGz(t, "ac", fakeBinary)
		}
		r.files[name] = b
		h := sha256.Sum256(b)
		sums[name] = hex.EncodeToString(h[:])
	}
	if mutate != nil {
		mutate(r.files, sums)
	}
	if _, ok := r.files["checksums.txt"]; !ok {
		var names []string
		for n := range sums {
			names = append(names, n)
		}
		sort.Strings(names)
		var sb strings.Builder
		for _, n := range names {
			fmt.Fprintf(&sb, "%s  %s\n", sums[n], n)
		}
		r.files["checksums.txt"] = []byte(sb.String())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.paths = append(r.paths, req.URL.Path)
		r.mu.Unlock()
		name := filepath.Base(req.URL.Path)
		dir := filepath.ToSlash(filepath.Dir(req.URL.Path))
		if dir != "/latest/download" && !strings.HasPrefix(dir, "/download/v") {
			http.NotFound(w, req)
			return
		}
		b, ok := r.files[name]
		if !ok || b == nil {
			http.NotFound(w, req)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	r.URL = srv.URL
	return r
}

func repoFile(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	return p
}
