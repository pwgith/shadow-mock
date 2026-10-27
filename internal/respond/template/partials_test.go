package template_test

import (
	"encoding/json"
	"testing"
)

// claimsTemplate is a response template written for WireMock: inline partials, a partial called
// with a context, block parameters, a null literal and defaults for every field.
const claimsTemplate = `{{#*inline "item"}}
{
  "identifier": "cc:Sn3wL0ssItemAbc123XyZ",
  "number": 2,
  "age": {{#if (neq this.age null)}}{{toJson this.age}}{{else}}5{{/if}},
  "brand": {{#if (neq this.brand null)}}{{toJson this.brand}}{{else}}"Sony"{{/if}},
  "repaired_replaced": {{#if (neq this.repaired_replaced null)}}{{toJson this.repaired_replaced}}{{else}}false{{/if}},
  "loss_estimate": {
    "amount": {{#if (neq this.loss_estimate.amount null)}}{{toJson this.loss_estimate.amount}}{{else}}123.00{{/if}},
    "currency": {{#if (neq this.loss_estimate.currency null)}}{{toJson this.loss_estimate.currency}}{{else}}"NZD"{{/if}}
  },
  "note": {{#if (neq this.note null)}}{{toJson this.note}}{{else}}"Headphones Test"{{/if}}
}
{{/inline}}
{{#*inline "incident"}}
{
  "incident_id": {{#if (neq this.incident_id null)}}{{toJson this.incident_id}}{{else}}"cc:Sj76J3X41Nj3hyUR-TSDH"{{/if}},
  "loss_party": {{#if (neq this.loss_party null)}}{{toJson this.loss_party}}{{else}}"insured"{{/if}},
  "type": "PropertyContentsIncident",
  "loss_items": [
    {{#each this.loss_items as |loss|}}{{> item loss}}{{#unless @last}},{{/unless}}{{else}}{{> item this}}{{/each}}
  ]
}
{{/inline}}
{{#with (parseJson request.body) as |req|}}
{
  "claim_id": {{#if (neq req.claim_id null)}}{{toJson req.claim_id}}{{else}}"C5328711"{{/if}},
  "incidents": [
    {{#each req.incidents as |inc|}}{{> incident inc}}{{#unless @last}},{{/unless}}{{else}}{{> incident req}}{{/each}}
  ]
}
{{/with}}`

func TestInlinePartialsRenderAWireMockTemplate(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, body, claim, brand, party string
		items                           int
	}{
		{"every field given", `{"claim_id":"C1","incidents":[{"incident_id":"i1","loss_party":"third party","loss_items":[{"brand":"Apple & Co","age":3,"loss_estimate":{"amount":50.5,"currency":"USD"}},{"note":"<b>"}]}]}`, "C1", "Apple & Co", "third party", 2},
		{"defaults fill what is missing", `{"incidents":[{"loss_items":[{}]}]}`, "C5328711", "Sony", "insured", 1},
		{"no incidents uses the default incident", `{"claim_id":"C9"}`, "C9", "Sony", "insured", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := data()
			d["request"] = map[string]any{"body": tt.body}
			out, err := engine().Render(claimsTemplate, d)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			var v map[string]any
			if err := json.Unmarshal([]byte(out), &v); err != nil {
				t.Fatalf("the output is not valid JSON: %v\n%s", err, out)
			}
			incidents, _ := v["incidents"].([]any)
			if len(incidents) == 0 {
				t.Fatalf("no incidents in %s", out)
			}
			first, _ := incidents[0].(map[string]any)
			items, _ := first["loss_items"].([]any)
			firstItem := map[string]any{}
			if len(items) > 0 {
				firstItem, _ = items[0].(map[string]any)
			}
			if v["claim_id"] != tt.claim || first["loss_party"] != tt.party {
				t.Errorf("claim %v, loss party %v; want %q and %q", v["claim_id"], first["loss_party"], tt.claim, tt.party)
			}
			if len(items) != tt.items || firstItem["brand"] != tt.brand {
				t.Errorf("loss items %v; want %d, the first with brand %q", items, tt.items, tt.brand)
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
