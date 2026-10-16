// Package desktop registers the launcher (.desktop file) and icon so the app
// shows up with its own icon in the GNOME dock and application grid. The
// binary does this by itself at startup: there is nothing to install.
package desktop

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AppID must match the GTK program name set in internal/gui, so GNOME links
// the running window to the launcher (Wayland app_id / X11 WM_CLASS).
const AppID = "videohub-control"

func dataHome() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

func launcherPath() string {
	return filepath.Join(dataHome(), "applications", AppID+".desktop")
}

func iconDir() string {
	return filepath.Join(dataHome(), "icons", "hicolor", "256x256", "apps")
}

// iconPath names the icon after its content. GNOME Shell caches icons by path
// for the whole session, so a new icon must get a new path to be shown.
func iconPath(icon []byte) string {
	sum := sha256.Sum256(icon)
	return filepath.Join(iconDir(), fmt.Sprintf("%s-%x.png", AppID, sum[:4]))
}

// staleIcons lists icons written by any version except keep.
func staleIcons(keep string) []string {
	var out []string
	matches, _ := filepath.Glob(filepath.Join(iconDir(), AppID+"*.png"))
	for _, m := range append(matches, legacyIcon()) {
		if m != keep {
			out = append(out, m)
		}
	}
	return out
}

// legacyIcon is where earlier versions put an SVG icon; removed on sight.
func legacyIcon() string {
	return filepath.Join(dataHome(), "icons", "hicolor", "scalable", "apps", AppID+".svg")
}

// quoteExec quotes a path for the Exec key (Desktop Entry spec).
func quoteExec(path string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	return `"` + r.Replace(path) + `"`
}

func entry(binary, icon string) string {
	return strings.Join([]string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=Videohub Control",
		"GenericName=Contrôle de routeur vidéo",
		"Comment=Routage des Blackmagic Videohub",
		"Exec=" + quoteExec(binary),
		// Absolute path: no icon-theme lookup, so no cache to go stale.
		"Icon=" + icon,
		"Terminal=false",
		"Categories=AudioVideo;Video;",
		"Keywords=videohub;blackmagic;router;routeur;routage;sdi;matrice;",
		"StartupWMClass=" + AppID,
		"StartupNotify=true",
		"",
	}, "\n")
}

// Ensure makes sure the launcher and icon exist for the current user and
// point at the given binary. It only writes when something differs (first
// run, binary moved, new icon), so it is cheap to call at every start.
// Returns whether anything was written.
func Ensure(binary string, icon []byte) (bool, error) {
	abs, err := filepath.Abs(binary)
	if err != nil {
		return false, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if strings.Contains(abs, string(filepath.Separator)+"go-build") {
		return false, nil // `go run` binary in a temp dir: don't register it
	}
	desktopFile, iconFile := launcherPath(), iconPath(icon)
	changed := false
	for _, f := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{{iconFile, icon, 0o644}, {desktopFile, []byte(entry(abs, iconFile)), 0o755}} {
		if current, err := os.ReadFile(f.path); err == nil && string(current) == string(f.data) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return changed, err
		}
		if err := os.WriteFile(f.path, f.data, f.mode); err != nil {
			return changed, fmt.Errorf("écriture de %s : %w", f.path, err)
		}
		changed = true
	}
	for _, old := range staleIcons(iconFile) {
		if os.Remove(old) == nil {
			changed = true
		}
	}
	if changed {
		go refreshCaches() // GNOME watches the folder itself; this is just a nudge
	}
	return changed, nil
}

// Uninstall removes the launcher and icon. Missing files are not an error.
func Uninstall() error {
	for _, p := range append(staleIcons(""), launcherPath()) {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	refreshCaches()
	return nil
}

// refreshCaches nudges the desktop to notice changes; both tools are optional.
func refreshCaches() {
	base := dataHome()
	if tool, err := exec.LookPath("update-desktop-database"); err == nil {
		exec.Command(tool, filepath.Join(base, "applications")).Run()
	}
	hicolor := filepath.Join(base, "icons", "hicolor")
	if _, err := os.Stat(filepath.Join(hicolor, "index.theme")); err == nil {
		if tool, err := exec.LookPath("gtk-update-icon-cache"); err == nil {
			exec.Command(tool, "-q", "-t", hicolor).Run()
		}
	}
}
