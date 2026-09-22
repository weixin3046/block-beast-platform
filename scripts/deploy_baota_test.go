package scripts

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
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

func TestBaotaPreservesManagedOriginsAndRollsBackUnhealthyRelease(t *testing.T) {
	for _, unhealthy := range []bool{false, true} {
		t.Run(fmt.Sprint(unhealthy), func(t *testing.T) {
			root := t.TempDir()
			release := filepath.Join(root, "releases", "new")
			old := filepath.Join(root, "releases", "old")
			os.MkdirAll(old, 0755)
			os.Symlink(old, filepath.Join(root, "current"))
			for _, name := range []string{"api", "worker", "realtime", "lulu-worker", "bootstrap-admin", "domainctl"} {
				writeFixture(t, filepath.Join(release, "bin", name), "#!/bin/sh\nexit 0\n", 0755)
			}
			writeFixture(t, filepath.Join(release, "scripts", "migrate.sh"), "#!/bin/sh\nexit 0\n", 0755)
			os.MkdirAll(filepath.Join(release, "migrations"), 0755)
			env := filepath.Join(root, "config.env")
			writeFixture(t, env, "APP_ENV=production\nBASE=old\n", 0640)
			writeFixture(t, filepath.Join(release, ".env.production"), "APP_ENV=production\nBASE=new\nMANAGED_ORIGINS_FILE=\n", 0600)
			managed := filepath.Join(root, "origins.json")
			writeFixture(t, managed, `{"version":1,"origins":["https://web.example.com"]}`, 0640)
			calls := filepath.Join(root, "calls")
			supervisor := filepath.Join(root, "supervisorctl")
			writeFixture(t, supervisor, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TEST_CALLS\"\n", 0755)
			tools := filepath.Join(root, "tools")
			writeFixture(t, filepath.Join(tools, "flock"), "#!/bin/sh\nexit 0\n", 0755)
			writeFixture(t, filepath.Join(tools, "mv"), "#!/usr/bin/env python3\nimport os,sys\nos.replace(sys.argv[-2],sys.argv[-1])\n", 0755)
			curl := "#!/bin/sh\nexit 0\n"
			if unhealthy {
				curl = "#!/bin/sh\nexit 1\n"
			}
			writeFixture(t, filepath.Join(tools, "curl"), curl, 0755)
			c := exec.Command("bash", "deploy-baota-remote.sh", release)
			c.Env = append(os.Environ(), "APP_DIR="+root, "ENV_FILE="+env, "SUPERVISORCTL="+supervisor, "PATH="+tools+":"+os.Getenv("PATH"), "TEST_CALLS="+calls, "BLOCK_BEAST_LOCK_FILE="+filepath.Join(root, "lock"), "MANAGED_ORIGINS_PATH="+managed)
			b, err := c.CombinedOutput()
			data, _ := os.ReadFile(env)
			link, _ := filepath.EvalSymlinks(filepath.Join(root, "current"))
			oldCanonical, _ := filepath.EvalSymlinks(old)
			releaseCanonical, _ := filepath.EvalSymlinks(release)
			if unhealthy {
				if err == nil {
					t.Fatalf("unhealthy release accepted %s", b)
				}
				if !strings.Contains(string(data), "BASE=old") || link != oldCanonical {
					t.Fatalf("not rolled back: %s %s %s", data, link, b)
				}
			} else {
				if err != nil {
					t.Fatalf("%v %s", err, b)
				}
				if !strings.Contains(string(data), "MANAGED_ORIGINS_FILE="+managed) || link != releaseCanonical {
					t.Fatalf("lost managed config: %s %s", data, link)
				}
			}
		})
	}
}
