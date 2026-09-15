package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// webDistFS holds the built React dashboard. In production it's the real
// `web/dist` output, copied here by Dockerfile.backend before `go build`
// runs; locally it's just the placeholder in webdist/index.html.
//
//go:embed webdist
var webDistFS embed.FS

// registerStaticUI serves the dashboard for any request that isn't an API
// route, so a single Fly app/Machine can serve both the API and the UI —
// this is what lets a self-serve cloud tenant get one deployed instance
// instead of two (backend + frontend).
func (s *Server) registerStaticUI(r *gin.Engine) {
	sub, err := fs.Sub(webDistFS, "webdist")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(sub))
	r.NoRoute(func(c *gin.Context) {
		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		// Serve the actual file for JS/CSS/assets; fall back to index.html
		// for any other path so client-side routing (React Router) works.
		if f, err := sub.Open(path); err == nil {
			f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}
