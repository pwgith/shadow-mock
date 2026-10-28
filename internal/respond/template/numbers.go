package template

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/mailgun/raymond/v2"
)

// Arithmetic and number formatting that follow WireMock, which does both with Java's BigDecimal
// and DecimalFormat (FR-10.5).

// decimal is a number with a fixed count of digits after the point, like Java's BigDecimal.
type decimal struct {
	unscaled *big.Int
	scale    int
}

// toDecimal reads a template value as a decimal. Whole numbers have scale 0, a number with a
// fraction has the scale of its shortest text, and text keeps the scale it is written with.
func toDecimal(v any) (decimal, bool) {
	switch t := v.(type) {
	case int:
		return decimal{big.NewInt(int64(t)), 0}, true
	case int64:
		return decimal{big.NewInt(t), 0}, true
	case float64:
		return parseDecimal(strconv.FormatFloat(t, 'f', -1, 64))
	case bool, nil:
		return decimal{}, false
	default:
		return parseDecimal(strings.TrimSpace(text(t)))
	}
}

func parseDecimal(s string) (decimal, bool) {
	whole, frac, _ := strings.Cut(s, ".")
	n, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok || s == "" || strings.ContainsAny(frac, "+-") {
		return decimal{}, false
	}
	return decimal{n, len(frac)}, true
}

// String writes the decimal in plain notation.
func (d decimal) String() string {
	s := new(big.Int).Abs(d.unscaled).String()
	if d.scale > 0 {
		if len(s) <= d.scale {
			s = strings.Repeat("0", d.scale-len(s)+1) + s
		}
		s = s[:len(s)-d.scale] + "." + s[len(s)-d.scale:]
	}
	if d.unscaled.Sign() < 0 {
		return "-" + s
	}
	return s
}

func pow10(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }

// rescale returns the unscaled value at a larger scale.
func (d decimal) rescale(scale int) *big.Int {
	return new(big.Int).Mul(d.unscaled, pow10(scale-d.scale))
}

func align(a, b decimal) (x, y *big.Int, scale int) {
	scale = max(a.scale, b.scale)
	return a.rescale(scale), b.rescale(scale), scale
}

// mathHelper implements {{math a '+' b}} for + - * / and %. As in WireMock, a division keeps the
// scale of the dividend and rounds half up, so 7 / 2 is 4 and 7.0 / 2 is 3.5.
func mathHelper(a, op, b any, _ *raymond.Options) any {
	x, ok1 := toDecimal(a)
	y, ok2 := toDecimal(b)
	if !ok1 || !ok2 {
		fail("math: operands %v and %v must be numbers", a, b)
	}
	var r decimal
	switch o := text(op); o {
	case "+", "-":
		p, q, scale := align(x, y)
		if o == "+" {
			r = decimal{p.Add(p, q), scale}
		} else {
			r = decimal{p.Sub(p, q), scale}
		}
	case "*":
		r = decimal{new(big.Int).Mul(x.unscaled, y.unscaled), x.scale + y.scale}
	case "/":
		if y.unscaled.Sign() == 0 {
			fail("math: division by zero")
		}
		r = decimal{divideHalfUp(x, y), x.scale}
	case "%":
		if y.unscaled.Sign() == 0 {
			fail("math: division by zero")
		}
		p, q, scale := align(x, y)
		r = decimal{p.Rem(p, q), scale}
	default:
		return inlineError("%s is not a valid mathematical operator", o)
	}
	return safe(r.String())
}

// divideHalfUp divides x by y and returns the quotient at x's scale, rounded half up.
func divideHalfUp(x, y decimal) *big.Int {
	num := new(big.Int).Mul(x.unscaled, pow10(y.scale))
	den := new(big.Int).Set(y.unscaled)
	return roundDiv(num, den, "HALF_UP")
}

// roundDiv divides num by den and rounds to a whole number with a java.math.RoundingMode.
func roundDiv(num, den *big.Int, mode string) *big.Int {
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if r.Sign() == 0 {
		return q
	}
	negative := (num.Sign() < 0) != (den.Sign() < 0)
	twice := new(big.Int).Abs(r)
	twice.Lsh(twice, 1)
	cmp := twice.Cmp(new(big.Int).Abs(den)) // the fraction against one half
	var awayFromZero bool
	switch mode {
	case "UP":
		awayFromZero = true
	case "DOWN":
		awayFromZero = false
	case "CEILING":
		awayFromZero = !negative
	case "FLOOR":
		awayFromZero = negative
	case "HALF_UP":
		awayFromZero = cmp >= 0
	case "HALF_DOWN":
		awayFromZero = cmp > 0
	default: // HALF_EVEN
		awayFromZero = cmp > 0 || cmp == 0 && q.Bit(0) == 1
	}
	if awayFromZero {
		if negative {
			return q.Sub(q, big.NewInt(1))
		}
		return q.Add(q, big.NewInt(1))
	}
	return q
}

// localeSymbols are the separators and the currency and percent layouts of a locale.
type localeSymbols struct {
	decimal, grouping string
	currencyPattern   string // the pattern of the "currency" format
	percentPattern    string // the pattern of the "percent" format
}

const (
	nbsp           = " "
	percentPattern = "#,##0%"
)

// locales lists the locales numberFormat knows. WireMock uses the JDK's locale data; these are the
// common ones, and an unknown locale falls back to English as the JDK does.
func locales() map[string]localeSymbols {
	en := localeSymbols{".", ",", "$#,##0.00", percentPattern}
	de := localeSymbols{",", ".", "#,##0.00" + nbsp + "€", "#,##0" + nbsp + "%"}
	fr := localeSymbols{",", " ", "#,##0.00" + nbsp + "€", "#,##0" + nbsp + "%"}
	es := localeSymbols{",", ".", "#,##0.00" + nbsp + "€", "#,##0" + nbsp + "%"}
	it := localeSymbols{",", ".", "#,##0.00" + nbsp + "€", percentPattern}
	return map[string]localeSymbols{
		"en": en, "en_US": en, "en_AU": en, "en_CA": en,
		"en_GB": {".", ",", "£#,##0.00", percentPattern},
		"de":    de, "de_DE": de,
		"fr": fr, "fr_FR": fr,
		"es": es, "es_ES": es,
		"it": it, "it_IT": it,
		"nl":    {",", ".", "€" + nbsp + "#,##0.00", percentPattern},
		"nl_NL": {",", ".", "€" + nbsp + "#,##0.00", percentPattern},
		"de_CH": {".", "’", "CHF" + nbsp + "#,##0.00", percentPattern},
		"en_IN": {".", ",", "₹#,##0.00", percentPattern},
		"ko":    {".", ",", "₩#,##0", percentPattern}, "ko_KR": {".", ",", "₩#,##0", percentPattern},
		"zh": {".", ",", "¥#,##0.00", percentPattern}, "zh_CN": {".", ",", "¥#,##0.00", percentPattern},
		"pt_BR": {",", ".", "R$" + nbsp + "#,##0.00", percentPattern},
		"ja":    {".", ",", "￥#,##0", percentPattern}, "ja_JP": {".", ",", "￥#,##0", percentPattern},
		"sv": {",", nbsp, "#,##0.00" + nbsp + "kr", "#,##0" + nbsp + "%"}, "sv_SE": {",", nbsp, "#,##0.00" + nbsp + "kr", "#,##0" + nbsp + "%"},
		"ru": {",", nbsp, "#,##0.00" + nbsp + "₽", "#,##0" + nbsp + "%"}, "ru_RU": {",", nbsp, "#,##0.00" + nbsp + "₽", "#,##0" + nbsp + "%"},
		"pl": {",", nbsp, "#,##0.00" + nbsp + "zł", "#,##0" + nbsp + "%"}, "pl_PL": {",", nbsp, "#,##0.00" + nbsp + "zł", "#,##0" + nbsp + "%"},
	}
}

// numberFormat implements {{numberFormat value format locale}} with the options of Java's
// NumberFormat: minimumIntegerDigits, maximumIntegerDigits, minimumFractionDigits,
// maximumFractionDigits, groupingUsed and roundingMode. The format is "integer", "currency",
// "percent" or a DecimalFormat pattern, and defaults to "#,##0.###"; the locale is written like
// de_DE. Like WireMock it prints nothing for a value that is not a number.
//
// raymond fixes the number of parameters, so the rewrite step calls one of the numberFormat__N
// helpers, by how many parameters the tag has.
func numberFormat(args []any, o *raymond.Options) any {
	if len(args) == 0 {
		return safe("")
	}
	d, ok := toNumber(args[0])
	if !ok {
		return safe("")
	}
	format := "#,##0.###"
	if len(args) > 1 {
		format = text(args[1])
	}
	sym := locales()["en"]
	if len(args) > 2 {
		name := text(args[2])
		if !validLocale(name) {
			fail("numberFormat: invalid locale format: %s", name)
		}
		if s, ok := locales()[name]; ok {
			sym = s
		}
	}
	switch format {
	case "integer":
		format = "#,##0"
	case "currency":
		format = sym.currencyPattern
	case "percent":
		format = sym.percentPattern
	}
	p := parsePattern(format)
	if o != nil {
		applyFormatOptions(&p, o)
	}
	return safe(formatDecimal(d, p, sym))
}

func numberFormatVariants() map[string]any {
	return map[string]any{
		"numberFormat":    func(args ...any) any { return numberFormat(args, nil) },
		"numberFormat__0": func(o *raymond.Options) any { return numberFormat(nil, o) },
		"numberFormat__1": func(v any, o *raymond.Options) any { return numberFormat([]any{v}, o) },
		"numberFormat__2": func(v, f any, o *raymond.Options) any { return numberFormat([]any{v, f}, o) },
		"numberFormat__3": func(v, f, l any, o *raymond.Options) any { return numberFormat([]any{v, f, l}, o) },
	}
}

// applyFormatOptions changes a pattern by the hash options.
func applyFormatOptions(p *pattern, o *raymond.Options) {
	if v, ok := number(o.HashProp("minimumIntegerDigits")); ok {
		p.minInt = int(v)
	}
	if v, ok := number(o.HashProp("maximumIntegerDigits")); ok {
		p.maxInt = int(v)
	}
	if v, ok := number(o.HashProp("minimumFractionDigits")); ok {
		p.minFrac = int(v)
		p.maxFrac = max(p.maxFrac, p.minFrac)
	}
	if v, ok := number(o.HashProp("maximumFractionDigits")); ok {
		p.maxFrac = int(v)
		p.minFrac = min(p.minFrac, p.maxFrac)
	}
	if g := o.HashProp("groupingUsed"); g != nil {
		switch {
		case !truthy(g):
			p.group = 0
		case p.group == 0:
			p.group = 3
		}
	}
	if m := strings.ToUpper(o.HashStr("roundingMode")); m != "" {
		p.rounding = m
	}
}

// toNumber reads a value that is a number, not text that looks like one.
func toNumber(v any) (decimal, bool) {
	switch v.(type) {
	case int, int64, float64:
		return toDecimal(v)
	default:
		return decimal{}, false
	}
}

func validLocale(s string) bool {
	lang, region, has := strings.Cut(s, "_")
	if len(lang) < 2 || len(lang) > 3 || strings.ToLower(lang) != lang {
		return false
	}
	return !has || len(region) == 2 && strings.ToUpper(region) == region
}

// pattern is a parsed DecimalFormat pattern.
type pattern struct {
	posPrefix, posSuffix string
	negPrefix, negSuffix string
	minInt, minFrac      int
	maxInt, maxFrac      int // maxInt is 0 for no limit
	group                int
	multiplier           int
	rounding             string // a java.math.RoundingMode name; HALF_EVEN when empty
}

func parsePattern(p string) pattern {
	pos, neg, hasNeg := strings.Cut(p, ";")
	out := pattern{multiplier: 1}
	prefix, body, suffix := splitPattern(pos)
	out.posPrefix, out.posSuffix = prefix, suffix
	switch body {
	case "": // no digit placeholders: the text is a prefix and a whole number follows
		out.posPrefix, out.minInt = pos, 1
	default:
		intPart, frac, _ := strings.Cut(body, ".")
		out.minInt = strings.Count(intPart, "0")
		if i := strings.LastIndex(intPart, ","); i >= 0 {
			out.group = len(intPart) - i - 1
		}
		out.minFrac = strings.Count(frac, "0")
		out.maxFrac = out.minFrac + strings.Count(frac, "#")
	}
	out.posPrefix = strings.ReplaceAll(out.posPrefix, "¤", "$")
	out.posSuffix = strings.ReplaceAll(out.posSuffix, "¤", "$")
	if strings.Contains(out.posPrefix+out.posSuffix, "%") {
		out.multiplier = 100
	} else if strings.Contains(out.posPrefix+out.posSuffix, "‰") {
		out.multiplier = 1000
	}
	out.negPrefix, out.negSuffix = "-"+out.posPrefix, out.posSuffix
	if hasNeg {
		np, _, ns := splitPattern(neg)
		out.negPrefix, out.negSuffix = np, ns
	}
	return out
}

// splitPattern splits a sub-pattern into its text before the digit placeholders, the placeholders,
// and the text after.
func splitPattern(p string) (prefix, body, suffix string) {
	isDigit := func(r rune) bool { return strings.ContainsRune("#0,.", r) }
	first := strings.IndexFunc(p, isDigit)
	if first < 0 {
		return p, "", ""
	}
	last := strings.LastIndexFunc(p, isDigit)
	return p[:first], p[first : last+1], p[last+1:]
}

func formatDecimal(d decimal, p pattern, sym localeSymbols) string {
	neg := d.unscaled.Sign() < 0
	n := new(big.Int).Set(d.unscaled)
	scale := d.scale
	if p.multiplier != 1 {
		n.Mul(n, big.NewInt(int64(p.multiplier)))
	}
	if scale > p.maxFrac { // round half even to the most fraction digits the pattern allows
		n = roundDiv(n, pow10(scale-p.maxFrac), p.rounding)
		scale = p.maxFrac
	}
	if n.Sign() < 0 {
		n.Neg(n)
	}
	digits := n.String()
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	intDigits, frac := digits[:len(digits)-scale], digits[len(digits)-scale:]
	frac = strings.TrimRight(frac, "0")
	if len(frac) < p.minFrac {
		frac += strings.Repeat("0", p.minFrac-len(frac))
	}
	intDigits = strings.TrimLeft(intDigits, "0")
	if p.maxInt > 0 && len(intDigits) > p.maxInt {
		intDigits = strings.TrimLeft(intDigits[len(intDigits)-p.maxInt:], "0")
	}
	if len(intDigits) < max(p.minInt, 1) {
		intDigits = strings.Repeat("0", max(p.minInt, 1)-len(intDigits)) + intDigits
	}
	if p.group > 0 {
		intDigits = groupDigits(intDigits, p.group, sym.grouping)
	}
	out := intDigits
	if frac != "" {
		out += sym.decimal + frac
	}
	if neg && n.Sign() != 0 {
		return p.negPrefix + out + p.negSuffix
	}
	return p.posPrefix + out + p.posSuffix
}

func groupDigits(s string, size int, sep string) string {
	var parts []string
	for len(s) > size {
		parts = append([]string{s[len(s)-size:]}, parts...)
		s = s[:len(s)-size]
	}
	return strings.Join(append([]string{s}, parts...), sep)
}
