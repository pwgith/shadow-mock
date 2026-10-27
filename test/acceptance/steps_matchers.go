package acceptance

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/pkg/extension"
)

// initMatchers registers the steps for custom matchers and multipart requests.
func (w *world) initMatchers(sc *godog.ScenarioContext) {
	sc.Step(`^Shadow Mock is built with the custom matcher "([^"]*)" that matches when query parameter "([^"]*)" is even$`, func(name, param string) error {
		w.matchers = append(w.matchers, extension.MatcherFunc(name, func(r extension.Request, _ map[string]any) bool {
			n, err := strconv.Atoi(r.Query.Get(param))
			return err == nil && n%2 == 0
		}))
		return nil
	})
	sc.Step(`^I send a multipart "([A-Z]+)" request to "([^"]*)" with parts:$`, func(method, path string, t *godog.Table) error {
		if len(t.Rows) < 2 {
			return fmt.Errorf("the table needs a header row and at least one part")
		}
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		for _, row := range t.Rows[1:] {
			if len(row.Cells) < 2 {
				return fmt.Errorf("each part needs a name and a content")
			}
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q", row.Cells[0].Value))
			pw, err := mw.CreatePart(h)
			if err != nil {
				return fmt.Errorf("create part: %w", err)
			}
			if _, err := pw.Write([]byte(row.Cells[1].Value)); err != nil {
				return fmt.Errorf("write part: %w", err)
			}
		}
		if err := mw.Close(); err != nil {
			return fmt.Errorf("close multipart body: %w", err)
		}
		r := newRequest(method, path)
		r.body = body.Bytes()
		r.header.Set("Content-Type", mw.FormDataContentType())
		return w.sendOne(r)
	})
}
