package template_test

import (
	"encoding/json"
	"testing"
)

// ordersTemplate is a response template of the kind written for WireMock: inline partials, a
// partial called with a context, block parameters, a null literal and a default for every field.
const ordersTemplate = `{{#*inline "parcel"}}
{
  "id": "parcel-0001",
  "number": 2,
  "weight_kg": {{#if (neq this.weight_kg null)}}{{toJson this.weight_kg}}{{else}}5{{/if}},
  "colour": {{#if (neq this.colour null)}}{{toJson this.colour}}{{else}}"Blue"{{/if}},
  "fragile": {{#if (neq this.fragile null)}}{{toJson this.fragile}}{{else}}false{{/if}},
  "value": {
    "amount": {{#if (neq this.value.amount null)}}{{toJson this.value.amount}}{{else}}123.00{{/if}},
    "currency": {{#if (neq this.value.currency null)}}{{toJson this.value.currency}}{{else}}"EUR"{{/if}}
  },
  "note": {{#if (neq this.note null)}}{{toJson this.note}}{{else}}"Packing test"{{/if}}
}
{{/inline}}
{{#*inline "shipment"}}
{
  "shipment_id": {{#if (neq this.shipment_id null)}}{{toJson this.shipment_id}}{{else}}"shipment-0001"{{/if}},
  "carrier": {{#if (neq this.carrier null)}}{{toJson this.carrier}}{{else}}"freight"{{/if}},
  "type": "StandardShipment",
  "parcels": [
    {{#each this.parcels as |p|}}{{> parcel p}}{{#unless @last}},{{/unless}}{{else}}{{> parcel this}}{{/each}}
  ]
}
{{/inline}}
{{#with (parseJson request.body) as |req|}}
{
  "order_id": {{#if (neq req.order_id null)}}{{toJson req.order_id}}{{else}}"O1000001"{{/if}},
  "shipments": [
    {{#each req.shipments as |ship|}}{{> shipment ship}}{{#unless @last}},{{/unless}}{{else}}{{> shipment req}}{{/each}}
  ]
}
{{/with}}`

func TestInlinePartialsRenderAWireMockTemplate(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, body, order, colour, carrier string
		parcels                            int
	}{
		{"every field given", `{"order_id":"O1","shipments":[{"shipment_id":"s1","carrier":"air","parcels":[{"colour":"Red & Gold","weight_kg":3,"value":{"amount":50.5,"currency":"USD"}},{"note":"<b>"}]}]}`, "O1", "Red & Gold", "air", 2},
		{"defaults fill what is missing", `{"shipments":[{"parcels":[{}]}]}`, "O1000001", "Blue", "freight", 1},
		{"no shipments uses the default shipment", `{"order_id":"O9"}`, "O9", "Blue", "freight", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := data()
			d["request"] = map[string]any{"body": tt.body}
			out, err := engine().Render(ordersTemplate, d)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			var v map[string]any
			if err := json.Unmarshal([]byte(out), &v); err != nil {
				t.Fatalf("the output is not valid JSON: %v\n%s", err, out)
			}
			shipments, _ := v["shipments"].([]any)
			if len(shipments) == 0 {
				t.Fatalf("no shipments in %s", out)
			}
			first, _ := shipments[0].(map[string]any)
			parcels, _ := first["parcels"].([]any)
			firstParcel := map[string]any{}
			if len(parcels) > 0 {
				firstParcel, _ = parcels[0].(map[string]any)
			}
			if v["order_id"] != tt.order || first["carrier"] != tt.carrier {
				t.Errorf("order %v, carrier %v; want %q and %q", v["order_id"], first["carrier"], tt.order, tt.carrier)
			}
			if len(parcels) != tt.parcels || firstParcel["colour"] != tt.colour {
				t.Errorf("parcels %v; want %d, the first with colour %q", parcels, tt.parcels, tt.colour)
			}
		})
	}
}

func TestInlinePartialEdgeCases(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, src, want string }{
		{"a partial can call another", `{{#*inline "a"}}<{{> b}}>{{/inline}}{{#*inline "b"}}x{{/inline}}{{> a}}`, "<x>"},
		{"single quoted name", `{{#*inline 'p'}}ok{{/inline}}{{> p}}`, "ok"},
		{"a partial sees the helpers", `{{#*inline "p"}}{{upper 'a'}}{{/inline}}{{> p}}`, "A"},
		{"the context passed in is this", `{{#*inline "p"}}{{this}}{{/inline}}{{> p 'z'}}`, "z"},
		{"definitions leave no output", `{{#*inline "p"}}x{{/inline}}tail`, "tail"},
	} {
		out, err := engine().Render(tt.src, data())
		if err != nil || out != tt.want {
			t.Errorf("%s: got %q (%v), want %q", tt.name, out, err, tt.want)
		}
	}
	if _, err := engine().Render(`{{#*inline "p"}}{{#if}}{{/inline}}{{> p}}`, data()); err == nil {
		t.Error("a bad partial body should be an error")
	}
}

func TestToJSONLeavesMarkupAlone(t *testing.T) {
	t.Parallel()
	out, err := engine().Render(`{{toJson name}}`, data())
	if err != nil || out != `"Ada & <Bob>"` {
		t.Errorf("toJson = %q (%v), want the text unescaped", out, err)
	}
}

// A JSON template often ends a tag with the closing brace of an object: {"a": {{x}}}. raymond
// reads the three braces as the end of an unescaped tag, so the engine writes the brace itself.
func TestATagFollowedByALiteralBrace(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, src, want string }{
		{"a value", `{"name": {{toJson name}}}`, `{"name": "Ada & <Bob>"}`},
		{"a block end", `{"a": {{#if flag}}1{{else}}2{{/if}}}`, `{"a": 1}`},
		{"a nested object", `{"o": {"n": {{toJson name}}}}`, `{"o": {"n": "Ada & <Bob>"}}`},
		{"an assigning tag", `{{parseJson body 'x'}}{"n": {{x.n}}}`, `{"n": 15}`},
		{"an unescaped tag", `{"n": {{{name}}}}`, `{"n": Ada & <Bob>}`},
		{"a tag with spaces then a brace", `{"n": {{ name }}}`, `{"n": Ada & <Bob>}`},
	} {
		out, err := engine().Render(tt.src, data())
		if err != nil || out != tt.want {
			t.Errorf("%s: %q gave %q (%v), want %q", tt.name, tt.src, out, err, tt.want)
		}
	}
}
