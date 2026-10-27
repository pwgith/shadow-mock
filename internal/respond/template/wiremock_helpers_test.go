package template_test

import (
	"fmt"
	"testing"
)

// The expected text in these cases was produced by WireMock 3.13.2 for the same templates.
func TestHelpersAsWireMockBehavesThem(t *testing.T) {
	t.Parallel()
	d := data()
	d["code"] = "123"
	d["word"] = "abc"
	d["missing"] = ""
	for _, tt := range []struct{ name, src, want string }{
		{"matches as a block", `{{#matches code '[0-9]+'}}numeric{{else}}no{{/matches}}`, "numeric"},
		{"matches as a block, else side", `{{#matches word '[0-9]+'}}numeric{{else}}no{{/matches}}`, "no"},
		{"matches as a block without else", `{{#matches word '[0-9]+'}}numeric{{/matches}}`, ""},
		{"matches as an expression", `{{#if (matches code '[0-9]+')}}yes{{else}}no{{/if}}`, "yes"},
		{"matches needs the whole text to match", `{{#if (matches 'a123' '[0-9]+')}}yes{{else}}no{{/if}}`, "no"},
		{"nested matches blocks", `{{#matches code '[0-9]+'}}{{#matches word '[a-z]+'}}both{{/matches}}{{/matches}}`, "both"},
		{"val uses the value when there is one", `[{{val code or='d'}}]`, "[123]"},
		{"val falls back with or", `[{{val missing or='d'}}]`, "[d]"},
		{"val falls back with default", `[{{val missing default='d'}}]`, "[d]"},
		{"arrayJoin", `{{arrayJoin ',' (array 'a' 'b')}}`, "a,b"},
		{"arrayAdd at the end", `{{arrayJoin ',' (arrayAdd (array 'b' 'c') 'a')}}`, "b,c,a"},
		{"arrayAdd at the start", `{{arrayJoin ', ' (arrayAdd (array 'b' 'c') 'a' position='start')}}`, "a, b, c"},
		{"range includes both ends", `{{#each (range 1 4)}}[{{this}}]{{/each}}`, "[1][2][3][4]"},
		{"range of one", `{{#each (range 3 3)}}[{{this}}]{{/each}}`, "[3]"},
	} {
		out, err := engine().Render(tt.src, d)
		if err != nil || out != tt.want {
			t.Errorf("%s: %s gave %q (%v), want %q", tt.name, tt.src, out, err, tt.want)
		}
	}
	if _, err := engine().Render(`{{range 4 1}}`, d); err == nil {
		t.Error("a range that runs backwards is an error, as in WireMock")
	}
}

// raymond prints nothing for a helper it does not know. WireMock fails the response, so a typo in
// a helper name is found instead of rendering as blanks.
func TestACallToAnUnknownHelperIsAnError(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		`{{nosuch 1 2}}`,
		`{{nosuch name='x'}}`,
		`{{#nosuch 1}}x{{/nosuch}}`,
		`{{#if (nosuch 1)}}x{{/if}}`,
		`{{upper (nosuch 'a')}}`,
		`{{#*inline "p"}}{{nosuch 1}}{{/inline}}{{> p}}`,
		`{{> p (nosuch 1)}}`,
	} {
		if out, err := engine().Render(src, data()); err == nil {
			t.Errorf("%s rendered %q, want an error", src, out)
		}
	}
}

func TestNamesThatAreNotHelperCallsStillRender(t *testing.T) {
	t.Parallel()
	d := data()
	for _, tt := range []struct{ src, want string }{
		{`[{{nosuch}}]`, "[]"}, // a bare name is a variable lookup, empty when missing
		{`{{#each items}}{{sku}}{{/each}}`, "ab"},
		{`{{#with name}}{{this}}{{/with}}`, "Ada & <Bob>"},
		{`{{#if flag}}y{{/if}}`, "y"},
		{`{{#unless flag}}n{{else}}y{{/unless}}`, "y"},
		{`{{#each items}}{{@index}}{{/each}}`, "01"},
	} {
		if out, err := engine().Render(tt.src, d); err != nil || out != tt.want {
			t.Errorf("%s gave %q (%v), want %q", tt.src, out, err, tt.want)
		}
	}
}

// Expected text from WireMock 3.13.2.
func TestMoreHelpersAsWireMockBehavesThem(t *testing.T) {
	t.Parallel()
	d := data()
	d["soap"] = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><k:GetClaim xmlns:k="urn:c"><k:ClaimId>42</k:ClaimId></k:GetClaim></s:Body></s:Envelope>`
	d["notsoap"] = `<e/>`
	d["form"] = `firstName=Ada%20L&lastName=Lovelace&x=1&x=2`
	for _, tt := range []struct{ name, src, want string }{
		{"date formats a parsed date", `{{date (parseDate '31/12/2025' format='dd/MM/yyyy') format='yyyy-MM-dd'}}`, "2025-12-31"},
		{"soapXPath looks inside the body", `{{soapXPath soap '/GetClaim/ClaimId/text()'}}`, "42"},
		{"soapXPath on a document with no body", `[{{soapXPath notsoap '/GetClaim/ClaimId/text()'}}]`, "[]"},
		{"formData with urlDecode", `{{formData form 'f' urlDecode=true}}{{f.firstName}} {{f.lastName}}`, "Ada L Lovelace"},
		{"formData without urlDecode leaves the text encoded", `{{formData form 'f'}}{{f.firstName}}`, "Ada%20L"},
		{"formData keeps repeated fields as a list", `{{formData form 'f'}}{{#each f.x}}[{{this}}]{{/each}}`, "[1][2]"},
		{"pickRandom of one value", `{{pickRandom 'only'}}`, "only"},
		{"pickRandom of a list of one", `{{pickRandom (array 'solo')}}`, "solo"},
	} {
		out, err := engine().Render(tt.src, d)
		if err != nil || out != tt.want {
			t.Errorf("%s: %s gave %q (%v), want %q", tt.name, tt.src, out, err, tt.want)
		}
	}
	for i := range 50 {
		out, err := engine().Render(`{{randomDecimal lower=-10.1 upper=12.5}}`, d)
		var v float64
		if _, serr := fmt.Sscan(out, &v); err != nil || serr != nil || v < -10.1 || v > 12.5 {
			t.Fatalf("randomDecimal #%d gave %q (%v, %v), want a number in range", i, out, err, serr)
		}
	}
	for range 30 {
		out, _ := engine().Render(`{{pickRandom 'red' 'green' 'blue'}}`, d)
		if out != "red" && out != "green" && out != "blue" {
			t.Fatalf("pickRandom gave %q", out)
		}
	}
}
