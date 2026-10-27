package acceptance

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// initData registers the steps that arrange global mappings, files and sessions.
func (w *world) initData(sc *godog.ScenarioContext) {
	sc.Step(`^the global mapping:$`, func(doc *godog.DocString) error {
		return w.record(func() error { return w.putGlobal(doc.Content) })
	})
	sc.Step(`^(\d+) global mappings exist$`, func(n int) error {
		return w.record(func() error {
			for i := range n {
				m := fmt.Sprintf(`{"request":{"url":"/bulk/%d"},"response":{"status":200}}`, i)
				if err := w.putGlobal(m); err != nil {
					return err
				}
			}
			return nil
		})
	})
	sc.Step(`^there are no global mappings$`, func() error {
		return w.record(func() error {
			return w.app.Service().DeleteAllMappings(context.Background(), store.Global(tenant.Local))
		})
	})
	sc.Step(`^the global mappings file "([^"]*)":$`, func(name string, doc *godog.DocString) error {
		return w.writeRoot(name, []byte(doc.Content))
	})
	sc.Step(`^I write the global mappings file "([^"]*)":$`, func(name string, doc *godog.DocString) error {
		return w.writeRoot(name, []byte(doc.Content))
	})
	sc.Step(`^(\d+) global mappings were loaded from files$`, func(n int) error {
		for i := range n {
			body := fmt.Sprintf(`{"request":{"url":"/file/%d"},"response":{"status":200}}`, i)
			if err := w.writeRoot(fmt.Sprintf("mappings/file-%d.json", i), []byte(body)); err != nil {
				return err
			}
		}
		if err := w.ensure(); err != nil {
			return err
		}
		return w.app.ReloadMappings(context.Background())
	})
	sc.Step(`^the file "([^"]*)":$`, func(name string, doc *godog.DocString) error {
		return w.writeRoot(name, []byte(doc.Content))
	})
	sc.Step(`^the file "([^"]*)" contains (\d+) bytes of PNG data$`, func(name string, n int) error {
		return w.writeRoot(name, pngBytes(n))
	})
	sc.Step(`^the folder "([^"]*)" contains (\d+) mapping files$`, func(dir string, n int) error {
		for i := range n {
			body := fmt.Sprintf(`{"request":{"urlPath":"/set/%d"},"response":{"status":200}}`, i)
			if err := w.writeRoot(fmt.Sprintf("%s/m%d.json", dir, i), []byte(body)); err != nil {
				return err
			}
		}
		return nil
	})
	w.initSessionData(sc)
}

func (w *world) putGlobal(mapping string) error {
	m, err := model.DecodeStubMapping([]byte(w.sub(mapping)))
	if err != nil {
		return fmt.Errorf("global mapping: %w", err)
	}
	_, err = w.app.Service().PutMapping(context.Background(), store.Global(tenant.Local), m)
	return err
}

// writeRoot writes a file under the scenario's root directory.
func (w *world) writeRoot(name string, data []byte) error {
	p := filepath.Join(w.root, filepath.FromSlash(strings.TrimPrefix(name, "./")))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return fmt.Errorf("create folder for %s: %w", name, err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

// pngBytes returns n bytes that start like a PNG file.
func pngBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	copy(b, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	return b
}

func (w *world) initSessionData(sc *godog.ScenarioContext) {
	sc.Step(`^a key-based shadow mock "([^"]*)"$`, func(name string) error {
		return w.record(func() error { return w.createSession("key-based", name, "{}") })
	})
	sc.Step(`^a key-based shadow mock "([^"]*)" with:$`, func(name string, doc *godog.DocString) error {
		return w.record(func() error { return w.createSession("key-based", name, doc.Content) })
	})
	sc.Step(`^a URL-based shadow mock "([^"]*)"$`, func(name string) error {
		return w.record(func() error { return w.createSession("url-based", name, "{}") })
	})
	sc.Step(`^a URL-based shadow mock "([^"]*)" with:$`, func(name string, doc *godog.DocString) error {
		return w.record(func() error { return w.createSession("url-based", name, doc.Content) })
	})
	sc.Step(`^(\d+) key-based shadow mocks$`, func(n int) error {
		return w.record(func() error {
			for i := range n {
				if err := w.createSession("key-based", "N"+strconv.Itoa(i), "{}"); err != nil {
					return err
				}
			}
			return nil
		})
	})
	sc.Step(`^the mapping in "([^"]*)":$`, func(name string, doc *godog.DocString) error {
		return w.record(func() error {
			_, err := w.mustAdmin("POST", "/sessions/${"+name+".id}/mappings", doc.Content, 201)
			return err
		})
	})
	sc.Step(`^a key-based shadow mock "([^"]*)" seeded from mapping set "([^"]*)"$`, func(name, set string) error {
		if err := w.writeRoot("mapping-sets/"+set+".json", []byte(`{"mappings":[{"request":{"urlPath":"/seeded"},"response":{"status":200}}]}`)); err != nil {
			return err
		}
		return w.record(func() error {
			if err := w.createSession("key-based", name, `{"seedFrom":{"mappingSet":"`+set+`"}}`); err != nil {
				return err
			}
			rep, err := w.mustAdmin("GET", "/sessions/${"+name+".id}/mappings", "", 200)
			if err != nil {
				return err
			}
			n, err := listLen(rep.body, "$.mappings")
			w.sessionCounts[name] = n
			return err
		})
	})
	sc.Step(`^the mapping set "([^"]*)" is changed$`, func(set string) error {
		return w.writeRoot("mapping-sets/"+set+".json",
			[]byte(`{"mappings":[{"request":{"urlPath":"/changed-1"},"response":{}},{"request":{"urlPath":"/changed-2"},"response":{}}]}`))
	})
	sc.Step(`^the mappings in "([^"]*)" are unchanged$`, func(name string) error {
		rep, err := w.mustAdmin("GET", "/sessions/${"+name+".id}/mappings", "", 200)
		if err != nil {
			return err
		}
		n, err := listLen(rep.body, "$.mappings")
		if err != nil {
			return err
		}
		if n != w.sessionCounts[name] {
			return fmt.Errorf("%s has %d mappings, want the %d it was seeded with", name, n, w.sessionCounts[name])
		}
		return nil
	})
	sc.Step(`^I create (\d+) key-based shadow mocks$`, func(n int) error { return w.createMany("key-based", n, "key") })
	sc.Step(`^I create (\d+) URL-based shadow mocks$`, func(n int) error { return w.createMany("url-based", n, "id") })
	sc.Step(`^all (\d+) (?:session keys|ids) are distinct$`, func(n int) error { return distinct(w.genKeys, n) })
	sc.Step(`^every id has at least 80 bits of randomness$`, func() error { return bitsAtLeast(w.genKeys, 80, "id") })
}

// createSession creates a session through the admin API and remembers it under name:
// ${name.id}, ${name.key} and ${name.baseUrl}.
func (w *world) createSession(kind, name, payload string) error {
	rep, err := w.mustAdmin("POST", "/sessions/"+kind, payload, 201)
	if err != nil {
		return err
	}
	var got map[string]any
	if err := json.Unmarshal(rep.body, &got); err != nil {
		return fmt.Errorf("parse created session: %w", err)
	}
	w.createdSession = got
	for field, v := range map[string]string{"id": "id", "key": "key", "baseUrl": "baseUrl"} {
		if s, ok := got[field].(string); ok {
			w.vars[name+"."+v] = s
		}
	}
	return nil
}

func (w *world) createMany(kind string, n int, field string) error {
	if err := w.ensure(); err != nil {
		return err
	}
	w.genKeys = w.genKeys[:0]
	for i := range n {
		name := "M" + strconv.Itoa(i)
		if err := w.createSession(kind, name, "{}"); err != nil {
			return err
		}
		w.genKeys = append(w.genKeys, w.vars[name+"."+field])
	}
	return nil
}

func listLen(body []byte, path string) (int, error) {
	res, err := jsonAt(body, path)
	if err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, fmt.Errorf("no value at %s", path)
	}
	list, ok := res[0].([]any)
	if !ok {
		return 0, fmt.Errorf("%s is not a list", path)
	}
	return len(list), nil
}

func distinct(vs []string, n int) error {
	seen := make(map[string]bool, len(vs))
	for _, v := range vs {
		if seen[v] {
			return fmt.Errorf("duplicate value %q", v)
		}
		seen[v] = true
	}
	if len(seen) != n {
		return fmt.Errorf("got %d distinct values, want %d", len(seen), n)
	}
	return nil
}

// bitsAtLeast checks that each value is long enough, in base32 characters of 5 bits each
// (after any smk_ or ses_ prefix), to carry the entropy the requirement names.
func bitsAtLeast(vs []string, bits int, what string) error {
	for _, v := range vs {
		v = strings.TrimPrefix(strings.TrimPrefix(v, "smk_"), "ses_")
		if len(v)*5 < bits {
			return fmt.Errorf("%s %q carries %d bits, want at least %d", what, v, len(v)*5, bits)
		}
	}
	return nil
}

// gzipBody compresses b.
func gzipBody(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	return buf.Bytes(), nil
}
