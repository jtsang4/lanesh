package server

import (
	"bytes"
	"embed"
	"net/http"
	"path"
	"regexp"
	"text/template"

	"github.com/jtsang4/lanesh/internal/version"
)

//go:embed scripts/install.sh scripts/install.ps1
var scripts embed.FS

var installTemplates = template.Must(template.ParseFS(scripts, "scripts/*"))

var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(-rc\.\d+)?$`)

// installVersion is the release that install scripts download: the server's
// own version for releases, otherwise the latest release.
func installVersion() string {
	if releaseVersion.MatchString(version.Version) {
		return version.Version
	}
	return "latest"
}

func (s *Server) installScript(w http.ResponseWriter, r *http.Request) {
	name := path.Base(r.URL.Path)
	var buf bytes.Buffer
	err := installTemplates.ExecuteTemplate(&buf, name, map[string]string{
		"Version": installVersion(), "ServerURL": s.publicURL(r),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(buf.Bytes())
}
