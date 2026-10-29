package admin

import (
	"net/http"

	"github.com/pwgith/shadow-mock/internal/apierr"
)

// routes registers every operation in api/openapi.yaml.
func (h *Handler) routes() {
	h.mappingRoutes()
	h.journalRoutes()
	h.systemRoutes()
	h.docsRoutes()
	h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		apierr.New(http.StatusNotFound, apierr.NotFound, "No admin endpoint %s %s.", r.Method, r.URL.Path).Write(w)
	})
}

// mappingRoutes registers mappings and files.
func (h *Handler) mappingRoutes() {
	// Mappings (WireMock-compatible, global and session scope).
	h.handle("GET", "/mappings", readAccess, h.listMappings)
	h.handle("POST", "/mappings", configAccess, h.createMapping)
	h.handle("DELETE", "/mappings", configAccess, h.deleteAllMappings)
	h.handle("GET", "/mappings/{stubMappingId}", readAccess, h.getMapping)
	h.handle("PUT", "/mappings/{stubMappingId}", configAccess, h.replaceMapping)
	h.handle("DELETE", "/mappings/{stubMappingId}", configAccess, h.deleteMapping)
	h.handle("POST", "/mappings/import", configAccess, h.importMappings)
	h.handle("POST", "/mappings/reset", configAccess, h.resetMappings)
	h.handleGlobal("POST", "/mappings/save", configAccess, h.saveMappings)
	h.handle("GET", "/mappings/unmatched", readAccess, h.unmatchedMappings)
	h.handle("DELETE", "/mappings/unmatched", configAccess, h.deleteUnmatchedMappings)
	h.handle("POST", "/mappings/find-by-metadata", readAccess, h.findByMetadata)
	h.handle("POST", "/mappings/remove-by-metadata", configAccess, h.removeByMetadata)
	h.handleGlobal("POST", "/mappings/validate", readAccess, h.validateMapping)
	h.handleGlobal("POST", "/mappings/dry-run", readAccess, h.dryRun)

	// Files.
	h.handle("GET", "/files", readAccess, h.listFiles)
	h.handle("GET", "/files/{fileId...}", readAccess, h.getFile)
	h.handle("PUT", "/files/{fileId...}", configAccess, h.putFile)
	h.handle("DELETE", "/files/{fileId...}", configAccess, h.deleteFile)
}

// journalRoutes registers the journal, verification, near misses and counters.
func (h *Handler) journalRoutes() {
	// Journal, verification and near misses.
	h.handle("GET", "/requests", readAccess, h.listRequests)
	h.handle("DELETE", "/requests", stateAccess, h.clearRequests)
	h.handle("GET", "/requests/{requestId}", readAccess, h.getRequest)
	h.handle("DELETE", "/requests/{requestId}", stateAccess, h.deleteRequest)
	h.handle("POST", "/requests/count", readAccess, h.countRequests)
	h.handle("POST", "/requests/find", readAccess, h.findRequests)
	h.handle("POST", "/requests/remove", stateAccess, h.removeRequests)
	h.handle("POST", "/requests/remove-by-metadata", stateAccess, h.removeRequestsByMetadata)
	h.handle("GET", "/requests/unmatched", readAccess, h.unmatchedRequests)
	h.handle("GET", "/requests/unmatched/near-misses", readAccess, h.unmatchedNearMisses)
	h.handle("POST", "/near-misses/request", readAccess, h.nearMissesForRequest)
	h.handle("POST", "/near-misses/request-pattern", readAccess, h.nearMissesForPattern)
	h.handleSession("POST", "/requests/export", readAccess, h.exportRequests)

	// Call counters, and the scenarios endpoints that are not supported (FR-4.4).
	h.handle("GET", "/counters", readAccess, h.listCounters)
	h.handle("POST", "/counters/reset", stateAccess, h.resetCounters)
	h.handle("PUT", "/counters/{stubMappingId}", stateAccess, h.setCounter)
	h.handle("DELETE", "/counters/{stubMappingId}", stateAccess, h.resetCounter)
	h.handle("", "/scenarios", readAccess, h.scenarios)
	h.handle("", "/scenarios/", readAccess, h.scenarios)
}

// systemRoutes registers recordings, settings, system, sessions and runtime endpoints.
func (h *Handler) systemRoutes() {
	// Recordings.
	h.handle("POST", "/recordings/start", configAccess, h.startRecording)
	h.handle("POST", "/recordings/stop", configAccess, h.stopRecording)
	h.handle("GET", "/recordings/status", readAccess, h.recordingStatus)
	h.handleGlobal("POST", "/recordings/snapshot", configAccess, h.snapshot)

	// Settings and system.
	h.handleGlobal("GET", "/settings", readAccess, h.getSettings)
	h.handleGlobal("POST", "/settings", configAccess, h.updateSettings)
	h.handleGlobal("PUT", "/settings", configAccess, h.updateSettings)
	h.handleGlobal("POST", "/reset", configAccess, h.resetAll)
	h.handleGlobal("POST", "/shutdown", configAccess, h.shutdown)
	h.handleGlobal("GET", "/version", readAccess, h.version)

	// Sessions.
	h.handleGlobal("GET", "/sessions", readAccess, h.listSessions)
	h.handleGlobal("POST", "/sessions/key-based", stateAccess, h.createKeyBased)
	h.handleGlobal("POST", "/sessions/url-based", stateAccess, h.createURLBased)
	h.handleSession("GET", "", readAccess, h.getSession)
	h.handleSession("PATCH", "", stateAccess, h.updateSession)
	h.handleSession("DELETE", "", stateAccess, h.deleteSession)
	h.handleSession("POST", "/keepalive", stateAccess, h.keepAlive)

	// Runtime.
	h.handleGlobal("GET", "/stats/live", readAccess, h.liveStats)
	h.handleGlobal("GET", "/usage", readAccess, h.usage)
}

// docsRoutes registers the API documents and the alias WireMock gives the mappings list.
func (h *Handler) docsRoutes() {
	h.handleGlobal("GET", "", readAccess, redirectToDirectory)
	h.handleGlobal("GET", "/{$}", readAccess, h.listMappings)
	h.handleGlobal("GET", "/docs", readAccess, h.docsIndex)
	h.handleGlobal("GET", "/docs/swagger", readAccess, h.openAPI)
	h.handleGlobal("GET", "/swagger-ui", readAccess, redirectToDirectory)
	h.handleGlobal("GET", "/swagger-ui/", readAccess, h.swaggerUI)
}
