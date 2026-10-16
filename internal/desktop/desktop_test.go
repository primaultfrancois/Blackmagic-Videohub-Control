package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallUninstall(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	bin := filepath.Join(t.TempDir(), "my apps", `vh"$`)
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)

	changed, err := Ensure(bin, []byte("<svg/>"))
	if err != nil || !changed {
		t.Fatalf("first Ensure: changed=%v err=%v", changed, err)
	}
	if changed, err := Ensure(bin, []byte("<svg/>")); err != nil || changed {
		t.Fatalf("second Ensure should be a no-op: changed=%v err=%v", changed, err)
	}
	launcher := launcherPath()
	raw, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"Icon=" + iconPath([]byte("<svg/>")) + "\n",
		"StartupWMClass=videohub-control\n",
		`Exec="` + filepath.Dir(bin) + `/vh\"\$"` + "\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("launcher missing %q:\n%s", want, text)
		}
	}
	if data, _ := os.ReadFile(iconPath([]byte("<svg/>"))); string(data) != "<svg/>" {
		t.Errorf("icon not written")
	}

	// New icon → new file name (beats GNOME's per-path cache), old one removed.
	if changed, _ := Ensure(bin, []byte("<png2/>")); !changed {
		t.Fatal("new icon should update the launcher")
	}
	if _, err := os.Stat(iconPath([]byte("<svg/>"))); !os.IsNotExist(err) {
		t.Error("old icon should be removed")
	}
	if iconPath([]byte("<svg/>")) == iconPath([]byte("<png2/>")) {
		t.Error("different icons must get different paths")
	}
	if raw, _ := os.ReadFile(launcher); !strings.Contains(string(raw), "Icon="+iconPath([]byte("<png2/>"))) {
		t.Errorf("launcher should point at the new icon:\n%s", raw)
	}
	Ensure(bin, []byte("<svg/>"))

	// Binary moved → launcher follows.
	moved := filepath.Join(t.TempDir(), "vh")
	os.WriteFile(moved, []byte("#!/bin/sh\n"), 0o755)
	if changed, _ := Ensure(moved, []byte("<svg/>")); !changed {
		t.Fatal("moved binary should update the launcher")
	}
	if raw, _ := os.ReadFile(launcher); !strings.Contains(string(raw), `Exec="`+moved+`"`) {
		t.Errorf("launcher not updated:\n%s", raw)
	}

	// `go run` binaries are never registered.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if changed, _ := Ensure("/tmp/go-build123/b001/exe/videohub-control", nil); changed {
		t.Error("go run binary should not be registered")
	}
	t.Setenv("XDG_DATA_HOME", filepath.Dir(filepath.Dir(launcher)))

	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(launcher); !os.IsNotExist(err) {
		t.Errorf("launcher still present")
	}
	if err := Uninstall(); err != nil {
		t.Errorf("second uninstall should be a no-op: %v", err)
	}
}
