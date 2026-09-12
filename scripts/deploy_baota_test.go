package scripts

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestBaotaArchiveMatchesServiceBinaryPaths(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	toolDir := filepath.Join(fixture, "tools")
	// Substitute only the build and network boundaries. Run the real packaging
	// script and inspect the resulting tarball, without connecting to any host.
	writeFixture(t, filepath.Join(toolDir, "go"), `#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    printf '#!/bin/sh\nexit 0\n' > "$2"
    chmod 0755 "$2"
    exit 0
  fi
  shift
done
exit 1
`, 0755)
	writeFixture(t, filepath.Join(toolDir, "ssh"), "#!/bin/sh\nexit 0\n", 0755)
	writeFixture(t, filepath.Join(toolDir, "scp"), "#!/bin/sh\nexec cp \"$1\" \"$TEST_ARCHIVE\"\n", 0755)
	archive := filepath.Join(fixture, "release.tar.gz")
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DEPLOY_HOST", "fixture-only")
	t.Setenv("VERSION", "layout-test")
	t.Setenv("SKIP_TEST", "1")
	t.Setenv("TEST_ARCHIVE", archive)
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "deploy-baota.sh"))
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("package release: %v\n%s", err, out)
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	entries := map[string]*tar.Header{}
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries[strings.TrimPrefix(header.Name, "./")] = header
	}
	for _, name := range []string{"api", "worker", "realtime", "lulu-worker", "bootstrap-admin"} {
		header := entries["bin/"+name]
		if header == nil || header.Mode&0111 == 0 || header.Size == 0 {
			t.Errorf("missing executable bin/%s in release archive", name)
		}
		if entries[name] != nil {
			t.Errorf("binary %s incorrectly packaged at release root", name)
		}
	}
	for _, path := range []string{"scripts/migrate.sh", "scripts/deploy-baota-remote.sh", "migrations/0001_core.sql"} {
		if entries[path] == nil {
			t.Errorf("release missing %s", path)
		}
	}
}

func TestBaotaRejectsInvalidReleaseBeforeStoppingServices(t *testing.T) {
	for _, layout := range []string{"root-binaries", "non-executable-api"} {
		t.Run(layout, func(t *testing.T) {
			fixture := t.TempDir()
			release := filepath.Join(fixture, "release")
			binaryDir := filepath.Join(release, "bin")
			if layout == "root-binaries" {
				binaryDir = release
			}
			for _, name := range []string{"api", "worker", "realtime", "lulu-worker", "bootstrap-admin"} {
				mode := os.FileMode(0755)
				if layout == "non-executable-api" && name == "api" {
					mode = 0644
				}
				writeFixture(t, filepath.Join(binaryDir, name), "#!/bin/sh\nexit 0\n", mode)
			}
			writeFixture(t, filepath.Join(release, "migrations", "fixture.sql"), "SELECT 1;", 0644)
			writeFixture(t, filepath.Join(release, "scripts", "migrate.sh"), "#!/bin/sh\nexit 0\n", 0755)
			envFile := filepath.Join(fixture, "config.env")
			writeFixture(t, envFile, "", 0600)
			supervisor := filepath.Join(fixture, "supervisorctl")
			writeFixture(t, supervisor, "#!/bin/sh\ntouch \"$TEST_SUPERVISOR_CALLED\"\nexit 1\n", 0755)
			marker := filepath.Join(fixture, "supervisor-called")
			t.Setenv("TEST_SUPERVISOR_CALLED", marker)
			t.Setenv("ENV_FILE", envFile)
			t.Setenv("SUPERVISORCTL", supervisor)
			t.Setenv("APP_DIR", fixture)
			out, err := exec.Command("bash", "deploy-baota-remote.sh", release).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "bin/api") {
				t.Fatalf("expected bin/api preflight rejection, got %v: %s", err, out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("invalid release touched running services")
			}
		})
	}
}
