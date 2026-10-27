package model_test

import (
	"testing"

	"github.com/pwgith/shadow-mock/internal/model"
)

// Mappings arrive over the admin API and from files, so decoding must reject bad input with an
// error and never panic.
func FuzzDecodeStubMapping(f *testing.F) {
	for _, s := range []string{
		`{"request":{"urlPath":"/a"},"response":{"status":200}}`,
		`{"request":{"urlPathTemplate":"/u/{id}","pathParameters":{"id":{"matches":"[0-9]+"}}},"response":{"jsonBody":{"a":[1,2]}}}`,
		`{"request":{"bodyPatterns":[{"equalToXml":"<a/>"},{"matchesJsonPath":"$.a"}]},"response":{"fault":"EMPTY_RESPONSE"}}`,
		`{"request":{"url":"/x"},"response":{"proxyBaseUrl":"http://u","chunkedDribbleDelay":{"numberOfChunks":0,"totalDuration":-1}},"callResponses":[{"calls":"2-4","response":{"status":500}},{"every":0,"response":{}}]}`,
		`{"request":{"url":"/x"},"response":{"delayDistribution":{"type":"lognormal","median":-1,"sigma":1e308}},"serveEventListeners":[{"name":"webhook","parameters":{"url":"x"}}]}`,
		`{"scenarioName":"s","request":{},"response":{}}`,
		`{"mappings":[{"request":{"urlPath":"/a"},"response":{"status":200}}]}`,
		`[]`, `null`, `{`, ``,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = model.DecodeStubMapping(data)
		_, _ = model.DecodeMappings(data)
	})
}
