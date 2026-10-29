// Package api embeds the OpenAPI documents so the binary can serve them (GET /__admin/docs).
package api

import _ "embed"

// OpenAPI is api/openapi.yaml, the contract of the engine's admin and runtime API.
//
//go:embed openapi.yaml
var OpenAPI []byte
