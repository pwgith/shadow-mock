package model

import (
	"bytes"
	"encoding/json"
	"slices"
	"sort"
	"strconv"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

// fields are the names WireMock 3.13 accepts in one part of a mapping, checked against a real
// server (FR-10.9). A name that is not listed is a misspelling, and WireMock answers 422 with a
// pointer to it. The Shadow Mock extensions (callResponses, gzipDisabled) are listed too.
type fields map[string]bool

func fieldSet(names ...string) fields {
	s := make(fields, len(names))
	for _, n := range names {
		s[n] = true
	}
	return s
}

func mappingFields() fields {
	return fieldSet("id", "uuid", "name", "persistent", "priority", "scenarioName", "requiredScenarioState",
		"newScenarioState", "postServeActions", "serveEventListeners", "metadata", "insertionIndex", "dirty",
		"request", "response", "callResponses")
}

func requestFields() fields {
	return fieldSet("url", "urlPattern", "urlPath", "urlPathPattern", "urlPathTemplate", "method", "scheme",
		"host", "port", "clientIp", "headers", "queryParameters", "pathParameters", "cookies", "basicAuthCredentials",
		"bodyPatterns", "multipartPatterns", "customMatcher", "formParameters", "name")
}

func responseDefinitionFields() fields {
	return fieldSet("status", "statusMessage", "headers", "additionalProxyRequestHeaders",
		"removeProxyRequestHeaders", "body", "base64Body", "jsonBody", "bodyFileName", "fixedDelayMilliseconds",
		"delayDistribution", "chunkedDribbleDelay", "fault", "fromConfiguredStub", "proxyBaseUrl",
		"proxyUrlPrefixToRemove", "transformers", "transformerParameters", "gzipDisabled")
}

// delayFields is what each delayDistribution type accepts; nil for a type it does not know, which
// Validate reports.
func delayFields(kind string) fields {
	switch kind {
	case "lognormal":
		return fieldSet("type", "median", "sigma", "maxValue")
	case "uniform":
		return fieldSet("type", "lower", "upper")
	case "fixed":
		return fieldSet("type", "milliseconds")
	}
	return nil
}

// isOperator reports whether a key makes an object a value pattern.
func isOperator(key string) bool {
	switch key {
	case "equalTo", "contains", "doesNotContain", "matches", "doesNotMatch", "absent", "before", "after",
		"equalToDateTime", "equalToNumber", "greaterThanNumber", "greaterThanEqualNumber", "lessThanNumber",
		"lessThanEqualNumber", "and", "or", "not", "equalToJson", "matchesJsonPath", "equalToXml", "matchesXPath",
		"anything", "binaryEqualTo", "matchesJsonSchema":
		return true
	}
	return false
}

// checker walks a mapping's JSON. With rejectUnknown unset it checks only that every matcher has
// an operator, which WireMock rejects whatever the setting.
type checker struct{ rejectUnknown bool }

// CheckKnownFields rejects a field a mapping may not have, with a 422 that points at it. A
// matcher object without an operator is rejected too.
func CheckKnownFields(data []byte) error {
	return checkShape(data, true)
}

func checkShape(data []byte, rejectUnknown bool) error {
	raw, ok := object(data)
	if !ok {
		return nil // DecodeStubMapping reports a body that is not an object
	}
	return checker{rejectUnknown}.mapping(raw)
}

func (c checker) mapping(raw map[string]json.RawMessage) error {
	if err := c.fields(raw, mappingFields(), "", "mapping"); err != nil {
		return err
	}
	if r, ok := object(raw["request"]); ok {
		if err := c.request(r, "/request"); err != nil {
			return err
		}
	}
	if r, ok := object(raw["response"]); ok {
		if err := c.response(r, "/response"); err != nil {
			return err
		}
	}
	for _, list := range []string{"postServeActions", "serveEventListeners"} {
		for _, a := range objects(raw[list]) {
			if err := c.fields(a, fieldSet("name", "parameters"), "/"+list, "action"); err != nil {
				return err
			}
		}
	}
	return c.callRules(raw["callResponses"])
}

func (c checker) callRules(raw json.RawMessage) error {
	for i, rule := range objects(raw) {
		ptr := "/callResponses/" + strconv.Itoa(i)
		if err := c.fields(rule, fieldSet("calls", "every", "response"), ptr, "call rule"); err != nil {
			return err
		}
		if r, ok := object(rule["response"]); ok {
			if err := c.response(r, ptr+"/response"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c checker) request(r map[string]json.RawMessage, ptr string) error {
	if err := c.fields(r, requestFields(), ptr, "request pattern"); err != nil {
		return err
	}
	for _, name := range []string{"host", "clientIp"} {
		if err := c.matcher(r[name], ptr+"/"+name); err != nil {
			return err
		}
	}
	for _, name := range []string{"headers", "queryParameters", "cookies", "pathParameters", "formParameters"} {
		multi := name == "headers" || name == "queryParameters"
		if err := c.matcherMap(r[name], ptr+"/"+name, multi); err != nil {
			return err
		}
	}
	if err := c.matcherList(r["bodyPatterns"], ptr+"/bodyPatterns"); err != nil {
		return err
	}
	if err := c.multipart(r["multipartPatterns"], ptr+"/multipartPatterns"); err != nil {
		return err
	}
	if a, ok := object(r["basicAuthCredentials"]); ok {
		if err := c.fields(a, fieldSet("username", "password"), ptr+"/basicAuthCredentials", "basic auth"); err != nil {
			return err
		}
	}
	if m, ok := object(r["customMatcher"]); ok {
		return c.fields(m, fieldSet("name", "parameters"), ptr+"/customMatcher", "custom matcher")
	}
	return nil
}

func (c checker) multipart(raw json.RawMessage, ptr string) error {
	for i, part := range objects(raw) {
		p := ptr + "/" + strconv.Itoa(i)
		if err := c.fields(part, fieldSet("matchingType", "name", "headers", "bodyPatterns"), p, "multipart pattern"); err != nil {
			return err
		}
		if err := c.matcherMap(part["headers"], p+"/headers", true); err != nil {
			return err
		}
		if err := c.matcherList(part["bodyPatterns"], p+"/bodyPatterns"); err != nil {
			return err
		}
	}
	return nil
}

func (c checker) response(r map[string]json.RawMessage, ptr string) error {
	if err := c.fields(r, responseDefinitionFields(), ptr, "response"); err != nil {
		return err
	}
	if d, ok := object(r["delayDistribution"]); ok {
		var kind string
		_ = json.Unmarshal(d["type"], &kind) //nolint:errcheck // a missing or odd type is reported by Validate
		if known := delayFields(kind); known != nil {
			if err := c.fields(d, known, ptr+"/delayDistribution", "delay distribution"); err != nil {
				return err
			}
		}
	}
	if d, ok := object(r["chunkedDribbleDelay"]); ok {
		return c.fields(d, fieldSet("numberOfChunks", "totalDuration"), ptr+"/chunkedDribbleDelay", "chunked dribble delay")
	}
	return nil
}

// fields reports the first field of obj (in name order) that is not in known.
func (c checker) fields(obj map[string]json.RawMessage, known fields, ptr, what string) error {
	if !c.rejectUnknown {
		return nil
	}
	for _, name := range sortedKeys(obj) {
		if !known[name] {
			return apierr.Invalid(ptr+"/"+name, "Unrecognized field %q in the %s. Check the spelling, or set mappings.unknownFields to keep to store fields that Shadow Mock does not know.", name, what)
		}
	}
	return nil
}

func (c checker) matcher(raw json.RawMessage, ptr string) error {
	if len(raw) == 0 {
		return nil
	}
	obj, isObject := object(raw)
	if isObject && slices.ContainsFunc(sortedKeys(obj), isOperator) {
		return c.nested(obj, ptr)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		compact.Reset()
		compact.Write(raw)
	}
	return apierr.Invalid(ptr, "%s is not a valid match operation", compact.String())
}

// nested checks the matchers inside and, or and not.
func (c checker) nested(obj map[string]json.RawMessage, ptr string) error {
	for _, op := range []string{"and", "or"} {
		for i, part := range array(obj[op]) {
			if err := c.matcher(part, ptr+"/"+op+"/"+strconv.Itoa(i)); err != nil {
				return err
			}
		}
	}
	if inner, ok := obj["not"]; ok {
		return c.matcher(inner, ptr+"/not")
	}
	return nil
}

// matcherMap checks a map of matchers. Headers and query parameters may hold several values, so a
// matcher there may also be hasExactly or includes, each with a list of matchers.
func (c checker) matcherMap(raw json.RawMessage, ptr string, multi bool) error {
	m, ok := object(raw)
	if !ok {
		return nil
	}
	for _, name := range sortedKeys(m) {
		if multi {
			if handled, err := c.multiValue(m[name], ptr+"/"+name); handled {
				if err != nil {
					return err
				}
				continue
			}
		}
		if err := c.matcher(m[name], ptr+"/"+name); err != nil {
			return err
		}
	}
	return nil
}

// multiValue checks a hasExactly or includes matcher, and reports whether the object is one.
func (c checker) multiValue(raw json.RawMessage, ptr string) (bool, error) {
	obj, ok := object(raw)
	if !ok {
		return false, nil
	}
	for _, op := range []string{"hasExactly", "includes"} {
		if _, has := obj[op]; has {
			return true, c.matcherList(obj[op], ptr+"/"+op)
		}
	}
	return false, nil
}

func (c checker) matcherList(raw json.RawMessage, ptr string) error {
	for i, m := range array(raw) {
		if err := c.matcher(m, ptr+"/"+strconv.Itoa(i)); err != nil {
			return err
		}
	}
	return nil
}

func object(raw []byte) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}

// array returns the elements of a JSON array, or nothing when raw is not one.
func array(raw json.RawMessage) []json.RawMessage {
	var list []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &list) != nil {
		return nil
	}
	return list
}

func objects(raw json.RawMessage) []map[string]json.RawMessage {
	var out []map[string]json.RawMessage
	for _, r := range array(raw) {
		if m, ok := object(r); ok {
			out = append(out, m)
		}
	}
	return out
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// withDefaultMethod returns the request object with method ANY filled in, as WireMock stores it.
func withDefaultMethod(request json.RawMessage) json.RawMessage {
	obj, ok := object(request)
	if !ok {
		return request
	}
	if _, has := obj["method"]; has {
		return request
	}
	obj["method"] = json.RawMessage(`"ANY"`)
	out, err := json.Marshal(obj)
	if err != nil {
		return request
	}
	return out
}
