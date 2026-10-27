package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the actual install recipe without building a provider or writing to
// the developer's home. The stub only supplies the build artifact; make, mkdir,
// and cp still run normally, including shell expansion and quoting.
func TestMakeInstall(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is required to test the local install workflow")
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, override := range []bool{false, true} {
		name := "default development directory"
		if override {
			name = "custom directory"
		}
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			home := filepath.Join(work, "home with spaces")
			bin := filepath.Join(work, "bin")
			if err := os.MkdirAll(bin, 0755); err != nil {
				t.Fatal(err)
			}
			stub := "#!/bin/sh\nset -eu\n[ \"$1\" = build ]\n[ \"$2\" = -o ]\nprintf '#!/bin/sh\\nexit 0\\n' > \"$3\"\nchmod +x \"$3\"\n"
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte(stub), 0755); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(work, "build with spaces", "terraform-provider-atlassian")
			if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
				t.Fatal(err)
			}
			installDir := filepath.Join(home, ".local", "share", "terraform-provider-atlassian", "dev")
			args := []string{"--no-print-directory", "-f", filepath.Join(root, "Makefile"), "install", "BINARY_NAME=" + binary, "HOME=" + home}
			if override {
				installDir = filepath.Join(work, "custom install with spaces")
				args = append(args, "INSTALL_DIR="+installDir)
			}
			cmd := exec.Command(makePath, args...)
			cmd.Dir = work
			// Keep the test independent of an invoking make or developer override.
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				switch key {
				case "PATH", "HOME", "MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES", "MAKELEVEL", "INSTALL_DIR", "BINARY_NAME":
					continue
				}
				cmd.Env = append(cmd.Env, entry)
			}
			cmd.Env = append(cmd.Env, "HOME="+home, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("make install failed: %v\n%s", err, output)
			}
			installed := filepath.Join(installDir, "terraform-provider-atlassian")
			info, err := os.Stat(installed)
			if err != nil {
				t.Fatalf("expected version-independent installation at %s: %v", installed, err)
			}
			if info.Mode()&0111 == 0 {
				t.Fatal("installed binary is not executable")
			}
			content, err := os.ReadFile(installed)
			if err != nil || string(content) != "#!/bin/sh\nexit 0\n" {
				t.Fatalf("installed artifact differs from build output: %q, %v", content, err)
			}
			for _, path := range []string{".terraform.d/plugins", ".terraformrc", ".tofurc"} {
				if _, err := os.Stat(filepath.Join(home, path)); !os.IsNotExist(err) {
					t.Fatalf("make install must not create %s: %v", path, err)
				}
			}
		})
	}
}
