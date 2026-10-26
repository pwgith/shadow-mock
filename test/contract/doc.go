// Package contract enforces the OpenAPI contract in api/openapi.yaml.
//
// It has two halves:
//
//   - Offline checks (always run): the spec is valid OpenAPI 3.1, every schema compiles,
//     every operation is well formed and has a live case, the mapping corpus and session
//     payloads conform to the schemas, and the negative corpus is rejected.
//   - Live checks (run when SHADOWMOCK_CONTRACT_BASE_URL is set): every operation is called
//     on a running engine and each request and response is validated against the spec.
//
// ValidatingTransport is exported so the acceptance suite can validate every admin call it
// makes against the same contract.
package contract
