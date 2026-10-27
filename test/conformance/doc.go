// Package conformance replays the mapping corpus in corpus/ against the engine.
//
// The corpus holds the WireMock and Shadow Mock mapping files and the request files that
// exercise them. These tests load each tree into an engine and send the requests the corpus
// describes, then check that the right mapping served each one and that the response is the
// one the mapping file defines. The expected responses are read from the mapping files, so the
// corpus is the single source of truth; the tests hold only the requests.
//
// Every mapping must have a case, or be listed as unsupported with a reason, so a new corpus
// file cannot be added without being tested.
package conformance
