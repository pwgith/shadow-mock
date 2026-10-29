package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/pwgith/shadow-mock/api"
	"github.com/pwgith/shadow-mock/internal/store"
)

// swaggerUIVersion is the swagger-ui-dist release the interactive page loads from a CDN. The
// OpenAPI document itself is served from the binary.
const swaggerUIVersion = "5.17.14"

const docsIndex = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Shadow Mock Documentation Index</title>
</head>
<body>
    <a href="docs/swagger" title="OpenAPI 3.1 spec">OpenAPI 3.1 spec</a><br />
    <a href="swagger-ui" title="Swagger UI">Swagger UI</a><br />
</body>
</html>
`

const swaggerPage = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Shadow Mock Admin API | Swagger UI</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@%[1]s/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@%[1]s/swagger-ui-bundle.js"></script>
<script>
    // The document sits next to this page, whatever prefix the admin API is served under.
    var spec = location.pathname.replace(/swagger-ui\/?$/, 'docs/swagger');
    window.ui = SwaggerUIBundle({url: spec, dom_id: '#swagger-ui'});
</script>
</body>
</html>
`

// specCache holds api/openapi.yaml as JSON, converted on first use.
type specCache struct {
	once sync.Once
	json []byte
	err  error
}

func (c *specCache) get() ([]byte, error) {
	c.once.Do(func() {
		var doc any
		if err := yaml.Unmarshal(api.OpenAPI, &doc); err != nil {
			c.err = fmt.Errorf("read the embedded OpenAPI document: %w", err)
			return
		}
		b, err := json.Marshal(doc)
		if err != nil {
			c.err = fmt.Errorf("convert the OpenAPI document to JSON: %w", err)
			return
		}
		c.json = b
	})
	return c.json, c.err
}

func writeHTML(w http.ResponseWriter, page string) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := fmt.Fprint(w, page)
	if err != nil {
		return fmt.Errorf("write page: %w", err)
	}
	return nil
}

func (h *Handler) docsIndex(w http.ResponseWriter, _ *http.Request, _ store.Scope) error {
	return writeHTML(w, docsIndex)
}

// redirectToDirectory sends /swagger-ui and /__admin to the same path with a slash, as WireMock does.
func redirectToDirectory(w http.ResponseWriter, r *http.Request, _ store.Scope) error {
	// The target is the last segment of the request's own path with a slash, which cannot leave the site.
	http.Redirect(w, r, path.Base(r.URL.Path)+"/", http.StatusFound) //nolint:gosec // see above
	return nil
}

func (h *Handler) swaggerUI(w http.ResponseWriter, _ *http.Request, _ store.Scope) error {
	return writeHTML(w, fmt.Sprintf(swaggerPage, swaggerUIVersion))
}

func (h *Handler) openAPI(w http.ResponseWriter, _ *http.Request, _ store.Scope) error {
	b, err := h.spec.get()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("write OpenAPI document: %w", err)
	}
	return nil
}
