package webui

import (
	"strings"
	"testing"
)

func TestPageIsSelfContained(t *testing.T) {
	page := Page()
	for _, ref := range []string{`href="app.css"`, `src="app.js"`} {
		if strings.Contains(page, ref) {
			t.Errorf("page still references %s", ref)
		}
	}
	for _, want := range []string{"<style>", "window.__vhNative", "Videohub Control"} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}
