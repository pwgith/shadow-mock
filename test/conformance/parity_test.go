package conformance_test

// parityCases covers corpus/wiremock/mappings/16-parity: behaviour found missing by comparing
// Shadow Mock with a real WireMock (docs/WIREMOCK_GAPS.md). Each case runs on both servers.
func parityCases() []tc {
	j := func(path, body string) call { return post(path, body, jsonCT, appJS) }
	return []tc{
		{want: "c018000101", call: j("/wm/parity/jsonpath/root-filter", `{ "status": "OPEN" }`)},
		{call: j("/wm/parity/jsonpath/root-filter", `{ "status": "CLOSED" }`)},
		{want: "c018000102", call: j("/wm/parity/jsonpath/size-function", `{ "things": [ 1, 2 ] }`)},
		{call: j("/wm/parity/jsonpath/size-function", `{ "things": [ 1 ] }`)},
		{want: "c018000103", call: j("/wm/parity/jsonpath/regex-filter", `{ "sku": "A100" }`)},
		{call: j("/wm/parity/jsonpath/regex-filter", `{ "sku": "B100" }`)},
		{want: "c018000104", call: j("/wm/parity/jsonpath/in-operator", `{ "tier": "gold" }`)},
		{call: j("/wm/parity/jsonpath/in-operator", `{ "tier": "bronze" }`)},
		{want: "c018000105", call: j("/wm/parity/jsonpath/nin-operator", `{ "tier": "gold" }`)},
		{call: j("/wm/parity/jsonpath/nin-operator", `{ "tier": "bronze" }`)},
		{want: "c018000106", call: j("/wm/parity/jsonpath/contains-operator", `{ "tags": [ "new", "vip" ] }`)},
		{call: j("/wm/parity/jsonpath/contains-operator", `{ "tags": [ "new" ] }`)},
		{want: "c018000107", call: j("/wm/parity/jsonpath/anyof-operator", `{ "tags": [ "staff" ] }`)},
		{call: j("/wm/parity/jsonpath/anyof-operator", `{ "tags": [ "other" ] }`)},
		{want: "c018000108", call: j("/wm/parity/jsonpath/subsetof-operator", `{ "tags": [ "vip", "new" ] }`)},
		{call: j("/wm/parity/jsonpath/subsetof-operator", `{ "tags": [ "vip", "other" ] }`)},
		{want: "c018000109", call: j("/wm/parity/jsonpath/size-operator", `{ "items": [ 1, 2 ] }`)},
		{call: j("/wm/parity/jsonpath/size-operator", `{ "items": [ 1, 2, 3 ] }`)},
		{want: "c018000110", call: j("/wm/parity/jsonpath/empty-operator", `{ "items": [] }`)},
		{call: j("/wm/parity/jsonpath/empty-operator", `{ "items": [ 1 ] }`)},
		{want: "c018000111", call: j("/wm/parity/jsonpath/length-function", `{ "items": [ 1, 2 ] }`)},
		{call: j("/wm/parity/jsonpath/length-function", `{ "other": 1 }`)},
		{want: "c018000112", call: j("/wm/parity/jsonpath/negative-index", `{ "items": [ 1, 2 ] }`)},
		{call: j("/wm/parity/jsonpath/negative-index", `{ "items": [] }`)},
		{want: "c018000113", call: j("/wm/parity/jsonpath/deep-scan", `{ "a": { "b": [ { "sku": "X" } ] } }`)},
		{call: j("/wm/parity/jsonpath/deep-scan", `{ "a": 1 }`)},
		{want: "c018000114", call: j("/wm/parity/jsonpath/number-vs-string", `{ "code": 42 }`)},
		{call: j("/wm/parity/jsonpath/number-vs-string", `{ "code": 43 }`)},
		{want: "c018000115", call: j("/wm/parity/jsonpath/missing-is-not-equal", `{ "other": 1 }`)},
		{want: "c018000116", call: j("/wm/parity/jsonpath/root-reference", `{ "limit": 5, "items": [ { "price": 9 } ] }`)},
		{call: j("/wm/parity/jsonpath/root-reference", `{ "limit": 50, "items": [ { "price": 9 } ] }`)},
		{want: "c018000117", call: j("/wm/parity/jsonunit/ignore-element", `{ "id": 1, "note": "anything" }`)},
		{want: "c018000117", call: j("/wm/parity/jsonunit/ignore-element", `{ "id": 1 }`)},
		{call: j("/wm/parity/jsonunit/ignore-element", `{ "id": 1, "extra": true }`)},
	}
}
