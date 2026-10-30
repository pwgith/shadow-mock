package template

import (
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"strconv"
	"strings"

	"github.com/mailgun/raymond/v2"
)

// EnableFaker adds the random helper, which WireMock's Faker extension provides: {{random 'Name.firstName'}}.
// WireMock hands the text to Datafaker as an expression. Shadow Mock has its own, smaller, word lists and
// understands the expressions for the common classes (see fakerGenerators); an expression it does not know
// is written in the response as [ERROR: …], as WireMock writes one Datafaker cannot evaluate.
func (e *Engine) EnableFaker() {
	e.AddHelpers(map[string]any{"random": randomFake})
}

func randomFake(expr any, _ *raymond.Options) any {
	s := text(expr)
	out, err := fake(s)
	if err != nil {
		return safe("[ERROR: Unable to evaluate the expression " + s + "]")
	}
	return safe(out)
}

// fake evaluates "Class.method" or "Class.method 'arg','arg'", with the method in camel or snake case and the
// whole wrapped in #{…} or not.
func fake(expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	expr = strings.TrimSuffix(strings.TrimPrefix(expr, "#{"), "}")
	path, args, _ := strings.Cut(strings.TrimSpace(expr), " ")
	gen, ok := fakerGenerators()[normalise(path)]
	if !ok {
		return "", fmt.Errorf("unknown expression %q", expr)
	}
	return gen(parseFakeArgs(args))
}

// normalise makes "Name.first_name", "name.firstName" and "NAME.FIRSTNAME" the same key.
func normalise(path string) string {
	return strings.ToLower(strings.ReplaceAll(path, "_", ""))
}

func parseFakeArgs(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ",") {
		if a = strings.Trim(strings.TrimSpace(a), `'"`); a != "" {
			out = append(out, a)
		}
	}
	return out
}

type generator func(args []string) (string, error)

// pickOne chooses an item at random.
func pickOne(items []string) string { return items[fakeInt(len(items))] }

func fakeInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		fail("random: %v", err)
	}
	return int(v.Int64())
}

func between(lo, hi int) int { return lo + fakeInt(hi-lo+1) }

func always(f func() string) generator { return func([]string) (string, error) { return f(), nil } }

// count reads an optional whole-number argument.
func count(args []string, fallback int) (int, error) {
	if len(args) == 0 {
		return fallback, nil
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 0 || n > 10000 {
		return 0, fmt.Errorf("bad count %q", args[0])
	}
	return n, nil
}

// fill replaces # with a digit and ? with a letter.
func fill(p string) string {
	var b strings.Builder
	for _, c := range p {
		switch c {
		case '#':
			b.WriteString(strconv.Itoa(fakeInt(10)))
		case '?':
			b.WriteByte("abcdefghijklmnopqrstuvwxyz"[fakeInt(26)])
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func words(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = pickOne(loremWords())
	}
	return out
}

func sentence() string {
	return capital(strings.Join(words(between(4, 10)), " ")) + "."
}

func paragraph() string {
	s := make([]string, between(3, 6))
	for i := range s {
		s[i] = sentence()
	}
	return strings.Join(s, " ")
}

func joined(n int, one func() string, sep string) string {
	out := make([]string, n)
	for i := range out {
		out[i] = one()
	}
	return strings.Join(out, sep)
}

func username() string {
	return strings.ToLower(pickOne(firstNames())) + "." + strings.ToLower(pickOne(lastNames()))
}

func domain() string { return strings.ToLower(pickOne(lastNames())) + "." + pickOne(topDomains()) }

func fullName() string { return pickOne(firstNames()) + " " + pickOne(lastNames()) }

func streetName() string { return pickOne(lastNames()) + " " + pickOne(streetSuffixes()) }

func streetAddress() string { return strconv.Itoa(between(1, 9999)) + " " + streetName() }

func ipV6() string {
	g := make([]string, 8)
	for i := range g {
		g[i] = fmt.Sprintf("%x", fakeInt(0x10000))
	}
	return strings.Join(g, ":")
}

func mac() string {
	g := make([]string, 6)
	for i := range g {
		g[i] = fmt.Sprintf("%02x", fakeInt(256))
	}
	return strings.Join(g, ":")
}

func fakeUUID() string { return UUID() }

func numberBetween(args []string) (string, error) {
	if len(args) != 2 {
		return "", errors.New("numberBetween needs two numbers")
	}
	lo, errLo := strconv.Atoi(args[0])
	hi, errHi := strconv.Atoi(args[1])
	if errLo != nil || errHi != nil || hi <= lo {
		return "", errors.New("bad range")
	}
	return strconv.Itoa(between(lo, hi-1)), nil
}

// fakerGenerators are the expressions the random helper knows, by lower-case "class.method" without underscores.
func fakerGenerators() map[string]generator {
	all := map[string]generator{}
	for _, class := range []func() map[string]generator{nameFakers, addressFakers, internetFakers, companyFakers, loremFakers, numberFakers, phonenumberFakers, boolFakers} {
		maps.Copy(all, class())
	}
	return all
}

func nameFakers() map[string]generator {
	return map[string]generator{
		"name.firstname":      always(func() string { return pickOne(firstNames()) }),
		"name.lastname":       always(func() string { return pickOne(lastNames()) }),
		"name.fullname":       always(fullName),
		"name.name":           always(fullName),
		"name.namewithmiddle": always(func() string { return pickOne(firstNames()) + " " + pickOne(lastNames()) + " " + pickOne(lastNames()) }),
		"name.prefix":         always(func() string { return pickOne([]string{"Mr.", "Mrs.", "Ms.", "Miss", "Dr."}) }),
		"name.suffix":         always(func() string { return pickOne([]string{"Jr.", "Sr.", "II", "III", "PhD", "MD"}) }),
		"name.username":       always(username),
		"name.title":          always(func() string { return pickOne(professions()) }),
	}
}

func addressFakers() map[string]generator {
	return map[string]generator{
		"address.city":          always(func() string { return pickOne(cities()) }),
		"address.cityname":      always(func() string { return pickOne(cities()) }),
		"address.streetname":    always(streetName),
		"address.streetaddress": always(streetAddress),
		"address.buildingnumber": always(func() string {
			return strconv.Itoa(between(1, 9999))
		}),
		"address.secondaryaddress": always(func() string { return "Apt. " + strconv.Itoa(between(1, 999)) }),
		"address.zipcode":          always(func() string { return fill("#####") }),
		"address.postcode":         always(func() string { return fill("#####") }),
		"address.state":            always(func() string { return pickOne(states()) }),
		"address.stateabbr":        always(func() string { return pickOne(stateAbbreviations()) }),
		"address.country":          always(func() string { return pickOne(countries()) }),
		"address.countrycode":      always(func() string { return pickOne(countryCodes()) }),
		"address.fulladdress": always(func() string {
			return streetAddress() + ", " + pickOne(cities()) + ", " + pickOne(stateAbbreviations()) + " " + fill("#####")
		}),
		"address.latitude":  always(func() string { return fmt.Sprintf("%.6f", float64(fakeInt(180000000))/1000000-90) }),
		"address.longitude": always(func() string { return fmt.Sprintf("%.6f", float64(fakeInt(360000000))/1000000-180) }),
	}
}

func internetFakers() map[string]generator {
	return map[string]generator{
		"internet.emailaddress":     always(func() string { return username() + "@" + domain() }),
		"internet.safeemailaddress": always(func() string { return username() + "@example." + pickOne([]string{"com", "org", "net"}) }),
		"internet.domainname":       always(domain),
		"internet.url":              always(func() string { return "https://www." + domain() + "/" + strings.ToLower(pickOne(loremWords())) }),
		"internet.username":         always(username),
		"internet.password": always(func() string {
			return pickChars(alphabetic+numeric, 12)
		}),
		"internet.ipv4address": always(func() string {
			return fmt.Sprintf("%d.%d.%d.%d", between(1, 254), fakeInt(256), fakeInt(256), between(1, 254))
		}),
		"internet.ipv6address": always(ipV6),
		"internet.macaddress":  always(mac),
		"internet.uuid":        always(fakeUUID),
	}
}

func companyFakers() map[string]generator {
	return map[string]generator{
		"company.name":     always(func() string { return pickOne(lastNames()) + " " + pickOne(companySuffixes()) }),
		"company.industry": always(func() string { return pickOne(industries()) }),
		"company.profession": always(func() string {
			return pickOne(professions())
		}),
		"company.buzzword": always(func() string { return pickOne(buzzwords()) }),
		"company.catchphrase": always(func() string {
			return capital(pickOne(buzzwords()) + " " + pickOne(buzzwords()) + " " + pickOne(buzzwords()))
		}),
		"company.suffix": always(func() string { return pickOne(companySuffixes()) }),
	}
}

func loremFakers() map[string]generator {
	return map[string]generator{
		"lorem.word": always(func() string { return pickOne(loremWords()) }),
		"lorem.words": func(a []string) (string, error) {
			n, err := count(a, 3)
			return strings.Join(words(n), " "), err
		},
		"lorem.sentence": always(sentence),
		"lorem.sentences": func(a []string) (string, error) {
			n, err := count(a, 3)
			return joined(n, sentence, " "), err
		},
		"lorem.paragraph": always(paragraph),
		"lorem.paragraphs": func(a []string) (string, error) {
			n, err := count(a, 3)
			return joined(n, paragraph, "\n\n"), err
		},
		"lorem.characters": func(a []string) (string, error) {
			n, err := count(a, 255)
			return pickChars(alphabetic+numeric, n), err
		},
	}
}

func numberFakers() map[string]generator {
	return map[string]generator{
		"number.randomdigit":        always(func() string { return strconv.Itoa(fakeInt(10)) }),
		"number.randomdigitnotzero": always(func() string { return strconv.Itoa(between(1, 9)) }),
		"number.digit":              always(func() string { return strconv.Itoa(fakeInt(10)) }),
		"number.digits": func(a []string) (string, error) {
			n, err := count(a, 8)
			return fill(strings.Repeat("#", n)), err
		},
		"number.randomnumber":  always(func() string { return strconv.Itoa(fakeInt(1 << 30)) }),
		"number.numberbetween": numberBetween,
	}
}

func phonenumberFakers() map[string]generator {
	return map[string]generator{
		"phonenumber.phonenumber": always(func() string { return fill("(###) ###-####") }),
		"phonenumber.cellphone":   always(func() string { return fill("###-###-####") }),
	}
}

func boolFakers() map[string]generator {
	return map[string]generator{
		"bool.bool":  always(func() string { return strconv.FormatBool(fakeInt(2) == 1) }),
		"color.name": always(func() string { return pickOne(colors()) }),
		"color.hex":  always(func() string { return "#" + pickChars(hexDigits, 6) }),
	}
}
