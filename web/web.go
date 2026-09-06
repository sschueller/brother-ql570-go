// Package web embeds the single-page web UI served by the ql570 daemon.
package web

import _ "embed"

// IndexHTML is the single-page label designer UI (HTML + inline CSS/JS),
// embedded into the ql570 binary so no separate web server is needed.
//
//go:embed index.html
var IndexHTML []byte
