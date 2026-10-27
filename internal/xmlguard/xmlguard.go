// Package xmlguard refuses XML that nests too deeply to be walked safely.
//
// Walking a document a million levels deep overflows the goroutine stack, and a stack overflow
// in Go is a fatal error that stops the whole process and cannot be recovered. A request body
// at the size limit can be that deep, so every place that parses request XML checks it first.
package xmlguard

import (
	"bytes"
	"encoding/xml"
)

// MaxDepth is how deeply nested a document may be. Real documents rarely pass a few dozen levels.
const MaxDepth = 1000

// DepthOK reports whether b nests no deeper than MaxDepth. A document cannot be deeper than it
// has opening angle brackets, which settles almost every body without parsing it.
func DepthOK(b []byte) bool {
	if bytes.Count(b, []byte("<")) <= MaxDepth {
		return true
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	depth := 0
	for {
		tok, err := dec.RawToken()
		if err != nil {
			return true // not well-formed; the parser will say so
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
			if depth > MaxDepth {
				return false
			}
		case xml.EndElement:
			depth--
		}
	}
}
