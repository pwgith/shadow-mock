package conformance_test

// parityCases covers corpus/wiremock/mappings/16-parity: behaviour found missing by comparing
// Shadow Mock with a real WireMock (docs/WIREMOCK_GAPS.md). Each case runs on both servers.
func parityCases() []tc {
	j := func(path, body string) call { return post(path, body, jsonCT, appJS) }
	return []tc{
		{want: "c016000101", call: j("/wm/parity/jsonpath/root-filter", `{ "status": "OPEN" }`)},
		{call: j("/wm/parity/jsonpath/root-filter", `{ "status": "CLOSED" }`)},
		{want: "c016000102", call: j("/wm/parity/jsonpath/size-function", `{ "things": [ 1, 2 ] }`)},
		{call: j("/wm/parity/jsonpath/size-function", `{ "things": [ 1 ] }`)},
		{want: "c016000103", call: j("/wm/parity/jsonpath/regex-filter", `{ "sku": "A100" }`)},
		{call: j("/wm/parity/jsonpath/regex-filter", `{ "sku": "B100" }`)},
		{want: "c016000104", call: j("/wm/parity/jsonpath/in-operator", `{ "tier": "gold" }`)},
		{call: j("/wm/parity/jsonpath/in-operator", `{ "tier": "bronze" }`)},
		{want: "c016000105", call: j("/wm/parity/jsonpath/nin-operator", `{ "tier": "gold" }`)},
		{call: j("/wm/parity/jsonpath/nin-operator", `{ "tier": "bronze" }`)},
		{want: "c016000106", call: j("/wm/parity/jsonpath/contains-operator", `{ "tags": [ "new", "vip" ] }`)},
		{call: j("/wm/parity/jsonpath/contains-operator", `{ "tags": [ "new" ] }`)},
		{want: "c016000107", call: j("/wm/parity/jsonpath/anyof-operator", `{ "tags": [ "staff" ] }`)},
		{call: j("/wm/parity/jsonpath/anyof-operator", `{ "tags": [ "other" ] }`)},
		{want: "c016000108", call: j("/wm/parity/jsonpath/subsetof-operator", `{ "tags": [ "vip", "new" ] }`)},
		{call: j("/wm/parity/jsonpath/subsetof-operator", `{ "tags": [ "vip", "other" ] }`)},
		{want: "c016000109", call: j("/wm/parity/jsonpath/size-operator", `{ "items": [ 1, 2 ] }`)},
		{call: j("/wm/parity/jsonpath/size-operator", `{ "items": [ 1, 2, 3 ] }`)},
		{want: "c016000110", call: j("/wm/parity/jsonpath/empty-operator", `{ "items": [] }`)},
		{call: j("/wm/parity/jsonpath/empty-operator", `{ "items": [ 1 ] }`)},
		{want: "c016000111", call: j("/wm/parity/jsonpath/length-function", `{ "items": [ 1, 2 ] }`)},
		{call: j("/wm/parity/jsonpath/length-function", `{ "other": 1 }`)},
		{want: "c016000112", call: j("/wm/parity/jsonpath/negative-index", `{ "items": [ 1, 2 ] }`)},
		{call: j("/wm/parity/jsonpath/negative-index", `{ "items": [] }`)},
		{want: "c016000113", call: j("/wm/parity/jsonpath/deep-scan", `{ "a": { "b": [ { "sku": "X" } ] } }`)},
		{call: j("/wm/parity/jsonpath/deep-scan", `{ "a": 1 }`)},
		{want: "c016000114", call: j("/wm/parity/jsonpath/number-vs-string", `{ "code": 42 }`)},
		{call: j("/wm/parity/jsonpath/number-vs-string", `{ "code": 43 }`)},
		{want: "c016000115", call: j("/wm/parity/jsonpath/missing-is-not-equal", `{ "other": 1 }`)},
		{want: "c016000116", call: j("/wm/parity/jsonpath/root-reference", `{ "limit": 5, "items": [ { "price": 9 } ] }`)},
		{call: j("/wm/parity/jsonpath/root-reference", `{ "limit": 50, "items": [ { "price": 9 } ] }`)},
		{want: "c016000117", call: j("/wm/parity/jsonunit/ignore-element", `{ "id": 1, "note": "anything" }`)},
		{want: "c016000117", call: j("/wm/parity/jsonunit/ignore-element", `{ "id": 1 }`)},
		{call: j("/wm/parity/jsonunit/ignore-element", `{ "id": 1, "extra": true }`)},
	}
}
