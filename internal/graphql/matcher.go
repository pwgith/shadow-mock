package graphql

import (
	"encoding/json"
	"reflect"

	"github.com/pwgith/shadow-mock/pkg/extension"
)

// MatcherName is the name WireMock's graphql-body-matcher extension is asked for by.
const MatcherName = "graphql-body-matcher"

// Matcher returns the request matcher a mapping asks for as
//
//	"customMatcher": { "name": "graphql-body-matcher", "parameters": { "query": "...", "variables": {...}, "operationName": "..." } }
//
// The request body must be a JSON object whose query is equal to the expected one (see Equal), whose
// variables are the same JSON as the expected variables, and whose operationName is the expected one.
// Variables or an operation name the mapping does not give must be absent from the request, as
// they must in WireMock's extension.
func Matcher() extension.RequestMatcher {
	return extension.MatcherFunc(MatcherName, match)
}

func match(r extension.Request, parameters map[string]any) bool {
	wantQuery, ok := parameters["query"].(string)
	if !ok {
		return false
	}
	var body struct {
		Query         *string         `json:"query"`
		Variables     json.RawMessage `json:"variables"`
		OperationName *string         `json:"operationName"`
	}
	if err := json.Unmarshal(r.Body, &body); err != nil || body.Query == nil {
		return false
	}
	if same, err := Equal(wantQuery, *body.Query); err != nil || !same {
		return false
	}
	return sameVariables(parameters["variables"], body.Variables) && sameOperation(parameters["operationName"], body.OperationName)
}

// sameVariables compares the expected variables with the request's. A missing value and a JSON null are
// both "absent".
func sameVariables(want any, got json.RawMessage) bool {
	var have any
	if len(got) > 0 {
		if err := json.Unmarshal(got, &have); err != nil {
			return false
		}
	}
	if want == nil || have == nil {
		return want == nil && have == nil
	}
	return reflect.DeepEqual(normalise(want), have)
}

// normalise puts a decoded parameter into the shapes encoding/json gives a request body, so the two compare.
func normalise(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func sameOperation(want any, got *string) bool {
	if want == nil || got == nil {
		return want == nil && got == nil
	}
	name, ok := want.(string)
	return ok && name == *got
}
