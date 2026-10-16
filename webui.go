// Package webui embeds the web interface shared by the Python server and the
// Go desktop binary, so both always ship the exact same UI.
package webui

import (
	_ "embed"
	"strings"
)

var (
	//go:embed videohub_control/web/index.html
	indexHTML string
	//go:embed videohub_control/web/app.css
	appCSS string
	//go:embed videohub_control/web/app.js
	appJS string

	// Icon is the application icon (dock, window, launcher): the in-app logo
	// (.logo in app.css) rendered at 256 px as PNG, the format GNOME Shell
	// handles most reliably.
	//go:embed packaging/videohub-control.png
	Icon []byte
)

// Page returns index.html with the stylesheet and script inlined, ready to
// load into a webview without any HTTP server.
func Page() string {
	page := strings.Replace(indexHTML, `<link rel="stylesheet" href="app.css">`, "<style>\n"+appCSS+"\n</style>", 1)
	return strings.Replace(page, `<script src="app.js"></script>`, "<script>\n"+appJS+"\n</script>", 1)
}
