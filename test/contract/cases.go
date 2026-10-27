package contract

import "net/http"

// Case is one live contract check: a request sent to a running engine, the statuses the
// OpenAPI contract allows for it, and the error code expected in the body, if any. The
// validating client checks every request and response against api/openapi.yaml as well.
type Case struct {
	// Name describes the check for test output.
	Name string
	// OperationID is the operation this case exercises. Every operation in the spec must
	// have at least one case (see TestEveryOperationHasALiveCase).
	OperationID string
	// Method and Path are sent relative to the engine base URL. Path, Body and Header values
	// may contain placeholders filled from fixtures: {sessionId}, {urlMockId},
	// {stubMappingId}, {sessionStubMappingId} and {fileId}, plus {runId}, which is unique per
	// run so match tokens never collide with an earlier run's.
	Method string
	Path   string
	// Body is sent as-is with ContentType (default application/json).
	Body        string
	ContentType string
	// Header adds request headers. The value may contain placeholders.
	Header map[string]string
	// WantStatus lists acceptable statuses. Several are allowed where the result depends
	// on mode (standalone vs SaaS), for example global writes.
	WantStatus []int
	// WantError, if set, is the `error` code the body must carry.
	WantError string
	// InvalidRequest marks a case that sends a body the contract forbids, to check that the
	// engine rejects it. Request-side violations are expected; response checks still apply.
	InvalidRequest bool
	// Needs lists fixtures that must exist before the case runs.
	Needs []Fixture
}

// Fixture is a precondition created once per live run.
type Fixture string

// Fixtures used by the live cases.
const (
	FixtureKeyBasedSession Fixture = "key-based session"
	FixtureURLBasedSession Fixture = "url-based session"
	FixtureGlobalMapping   Fixture = "global mapping"
	FixtureSessionMapping  Fixture = "session mapping"
	FixtureSessionFile     Fixture = "session file"
)

const (
	unknownMappingID = "00000000-0000-4000-8000-000000000000"
	unknownSessionID = "ses_00000000000000000000000000"
)

func statuses(s ...int) []int { return s }

// simpleMapping is a minimal valid mapping body.
const simpleMapping = `{"request":{"method":"GET","urlPath":"/contract/simple"},"response":{"status":200,"body":"ok"}}`

// callCountMapping exercises the callResponses extension.
const callCountMapping = `{"request":{"method":"POST","urlPath":"/contract/retry"},"response":{"status":200},` +
	`"callResponses":[{"calls":"1-2","response":{"status":503}},{"every":3,"response":{"status":500}}]}`

// scenarioMapping must be rejected (FR-4.4).
const scenarioMapping = `{"scenarioName":"flow","requiredScenarioState":"Started",` +
	`"request":{"urlPath":"/contract/scenario"},"response":{"status":200}}`

const requestPattern = `{"method":"ANY","urlPathPattern":"/contract/.*"}`

const sampleRequest = `{"method":"GET","url":"/contract/simple","headers":{"Accept":"text/plain"}}`

// Cases is the live contract suite. Order matters only within a fixture's lifetime;
// fixtures are created before the first case that needs them.
var Cases = []Case{
	// ── Mappings (global scope) ──
	{Name: "list mappings", OperationID: "listMappings", Method: http.MethodGet, Path: "/__admin/mappings?limit=10&offset=0", WantStatus: statuses(200)},
	{Name: "create mapping (global write)", OperationID: "createMapping", Method: http.MethodPost, Path: "/__admin/mappings", Body: simpleMapping, WantStatus: statuses(201, 403)},
	{Name: "create mapping rejects scenario fields", OperationID: "createMapping", Method: http.MethodPost, Path: "/__admin/mappings", Body: scenarioMapping, Header: map[string]string{"X-Shadow-Mock-Key": "{sessionId}"}, InvalidRequest: true, WantStatus: statuses(422), WantError: "scenarios_not_supported", Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "create mapping rejects an invalid mapping", OperationID: "createMapping", Method: http.MethodPost, Path: "/__admin/mappings", Body: `{"request":{"url":"/a","urlPath":"/a"},"response":{"status":200}}`, Header: map[string]string{"X-Shadow-Mock-Key": "{sessionId}"}, WantStatus: statuses(422), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "delete all mappings (global write)", OperationID: "deleteAllMappings", Method: http.MethodDelete, Path: "/__admin/mappings", Header: map[string]string{"X-Shadow-Mock-Key": "{sessionId}"}, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "get mapping", OperationID: "getMapping", Method: http.MethodGet, Path: "/__admin/mappings/{stubMappingId}", WantStatus: statuses(200), Needs: []Fixture{FixtureGlobalMapping}},
	{Name: "get unknown mapping", OperationID: "getMapping", Method: http.MethodGet, Path: "/__admin/mappings/" + unknownMappingID, WantStatus: statuses(404)},
	{Name: "replace mapping (global write)", OperationID: "replaceMapping", Method: http.MethodPut, Path: "/__admin/mappings/{stubMappingId}", Body: simpleMapping, WantStatus: statuses(200, 403), Needs: []Fixture{FixtureGlobalMapping}},
	{Name: "delete mapping (global write)", OperationID: "deleteMapping", Method: http.MethodDelete, Path: "/__admin/mappings/" + unknownMappingID, WantStatus: statuses(404, 403)},
	{Name: "import mappings (global write)", OperationID: "importMappings", Method: http.MethodPost, Path: "/__admin/mappings/import", Body: `{"mappings":[` + simpleMapping + `],"importOptions":{"duplicatePolicy":"IGNORE","deleteAllNotInImport":false}}`, WantStatus: statuses(200, 403)},
	{Name: "reset mappings (global write)", OperationID: "resetMappings", Method: http.MethodPost, Path: "/__admin/mappings/reset", Header: map[string]string{"X-Shadow-Mock-Key": "{sessionId}"}, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "save mappings (standalone only)", OperationID: "saveMappings", Method: http.MethodPost, Path: "/__admin/mappings/save", WantStatus: statuses(200, 404)},
	{Name: "list unmatched mappings", OperationID: "listUnmatchedMappings", Method: http.MethodGet, Path: "/__admin/mappings/unmatched", WantStatus: statuses(200)},
	{Name: "find mappings by metadata", OperationID: "findMappingsByMetadata", Method: http.MethodPost, Path: "/__admin/mappings/find-by-metadata", Body: `{"matchesJsonPath":{"expression":"$.team","equalTo":"claims"}}`, WantStatus: statuses(200)},
	{Name: "remove mappings by metadata (global write)", OperationID: "removeMappingsByMetadata", Method: http.MethodPost, Path: "/__admin/mappings/remove-by-metadata", Body: `{"matchesJsonPath":{"expression":"$.team","equalTo":"nobody"}}`, WantStatus: statuses(200, 403)},
	{Name: "validate a valid mapping", OperationID: "validateMapping", Method: http.MethodPost, Path: "/__admin/mappings/validate", Body: callCountMapping, WantStatus: statuses(200)},
	{Name: "validate an invalid mapping still returns 200", OperationID: "validateMapping", Method: http.MethodPost, Path: "/__admin/mappings/validate", Body: scenarioMapping, WantStatus: statuses(200)},
	{Name: "dry-run a request", OperationID: "dryRunRequest", Method: http.MethodPost, Path: "/__admin/mappings/dry-run", Body: `{"request":` + sampleRequest + `}`, WantStatus: statuses(200)},

	// ── Files ──
	{Name: "list files", OperationID: "listFiles", Method: http.MethodGet, Path: "/__admin/files", WantStatus: statuses(200)},
	{Name: "get unknown file", OperationID: "getFile", Method: http.MethodGet, Path: "/__admin/files/contract-missing.json", WantStatus: statuses(404)},
	{Name: "put file (global write)", OperationID: "putFile", Method: http.MethodPut, Path: "/__admin/files/contract.txt", Body: "hello", ContentType: "application/octet-stream", WantStatus: statuses(200, 403)},
	{Name: "delete unknown file (global write)", OperationID: "deleteFile", Method: http.MethodDelete, Path: "/__admin/files/contract-missing.json", WantStatus: statuses(404, 403)},

	// ── Journal and verification ──
	{Name: "list requests", OperationID: "listRequests", Method: http.MethodGet, Path: "/__admin/requests?limit=5", WantStatus: statuses(200)},
	{Name: "clear requests", OperationID: "clearRequests", Method: http.MethodDelete, Path: "/__admin/requests", Header: map[string]string{"X-Shadow-Mock-Key": "{sessionId}"}, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "get unknown request", OperationID: "getRequest", Method: http.MethodGet, Path: "/__admin/requests/" + unknownMappingID, WantStatus: statuses(404)},
	{Name: "delete unknown request", OperationID: "deleteRequest", Method: http.MethodDelete, Path: "/__admin/requests/" + unknownMappingID, WantStatus: statuses(404)},
	{Name: "count requests", OperationID: "countRequests", Method: http.MethodPost, Path: "/__admin/requests/count", Body: requestPattern, WantStatus: statuses(200)},
	{Name: "find requests", OperationID: "findRequests", Method: http.MethodPost, Path: "/__admin/requests/find", Body: requestPattern, WantStatus: statuses(200)},
	{Name: "remove requests", OperationID: "removeRequests", Method: http.MethodPost, Path: "/__admin/requests/remove", Body: requestPattern, Header: map[string]string{"X-Shadow-Mock-Key": "{sessionId}"}, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "list unmatched requests", OperationID: "listUnmatchedRequests", Method: http.MethodGet, Path: "/__admin/requests/unmatched", WantStatus: statuses(200)},
	{Name: "near misses for unmatched requests", OperationID: "nearMissesForUnmatchedRequests", Method: http.MethodGet, Path: "/__admin/requests/unmatched/near-misses", WantStatus: statuses(200)},
	{Name: "near misses for a request", OperationID: "nearMissesForRequest", Method: http.MethodPost, Path: "/__admin/near-misses/request", Body: sampleRequest, WantStatus: statuses(200)},
	{Name: "near misses for a request pattern", OperationID: "nearMissesForRequestPattern", Method: http.MethodPost, Path: "/__admin/near-misses/request-pattern", Body: requestPattern, WantStatus: statuses(200)},

	// ── Call counters ──
	{Name: "list counters", OperationID: "listCounters", Method: http.MethodGet, Path: "/__admin/counters", WantStatus: statuses(200)},
	{Name: "reset counters", OperationID: "resetCounters", Method: http.MethodPost, Path: "/__admin/counters/reset", Header: map[string]string{"X-Shadow-Mock-Key": "{sessionId}"}, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "set a counter", OperationID: "setCounter", Method: http.MethodPut, Path: "/__admin/counters/{stubMappingId}", Body: `{"count":9}`, WantStatus: statuses(200), Needs: []Fixture{FixtureGlobalMapping}},
	{Name: "set a counter to a negative value", OperationID: "setCounter", Method: http.MethodPut, Path: "/__admin/counters/{stubMappingId}", Body: `{"count":-1}`, InvalidRequest: true, WantStatus: statuses(422), Needs: []Fixture{FixtureGlobalMapping}},
	{Name: "reset one counter", OperationID: "resetCounter", Method: http.MethodDelete, Path: "/__admin/counters/{stubMappingId}", WantStatus: statuses(200), Needs: []Fixture{FixtureGlobalMapping}},
	{Name: "scenarios are not supported", OperationID: "scenariosNotSupported", Method: http.MethodGet, Path: "/__admin/scenarios", WantStatus: statuses(404), WantError: "not_supported"},

	// ── Recordings ──
	{Name: "recording status", OperationID: "getRecordingStatus", Method: http.MethodGet, Path: "/__admin/recordings/status", WantStatus: statuses(200)},
	{Name: "start recording (global write)", OperationID: "startRecording", Method: http.MethodPost, Path: "/__admin/recordings/start", Body: `{"targetBaseUrl":"http://localhost:8089"}`, WantStatus: statuses(200, 403)},
	{Name: "stop recording (global write)", OperationID: "stopRecording", Method: http.MethodPost, Path: "/__admin/recordings/stop", WantStatus: statuses(200, 403)},
	{Name: "snapshot (global write)", OperationID: "snapshotRecording", Method: http.MethodPost, Path: "/__admin/recordings/snapshot", Body: `{"persist":false}`, WantStatus: statuses(200, 403)},

	// ── Settings and system ──
	{Name: "get settings", OperationID: "getSettings", Method: http.MethodGet, Path: "/__admin/settings", WantStatus: statuses(200)},
	{Name: "update settings (global write)", OperationID: "updateSettings", Method: http.MethodPost, Path: "/__admin/settings", Body: `{"fixedDelay":0}`, WantStatus: statuses(200, 403)},
	{Name: "reset all (global write)", OperationID: "resetAll", Method: http.MethodPost, Path: "/__admin/reset", Header: map[string]string{destructiveHeader: "skip-in-shared-env"}, WantStatus: statuses(200, 403)},
	{Name: "admin health", OperationID: "getAdminHealth", Method: http.MethodGet, Path: "/__admin/health", WantStatus: statuses(200)},
	{Name: "version", OperationID: "getVersion", Method: http.MethodGet, Path: "/__admin/version", WantStatus: statuses(200)},

	// ── Sessions ──
	{Name: "list sessions", OperationID: "listSessions", Method: http.MethodGet, Path: "/__admin/sessions?kind=key-based", WantStatus: statuses(200)},
	{Name: "create key-based session", OperationID: "createKeyBasedSession", Method: http.MethodPost, Path: "/__admin/sessions/key-based", Body: `{"name":"contract","ttlSeconds":600,"tokens":["CONTRACT-TOKEN-1-{runId}"]}`, WantStatus: statuses(201)},
	{Name: "create key-based session with too long a TTL", OperationID: "createKeyBasedSession", Method: http.MethodPost, Path: "/__admin/sessions/key-based", Body: `{"ttlSeconds":86401}`, InvalidRequest: true, WantStatus: statuses(422), WantError: "ttl_exceeds_maximum"},
	{Name: "create key-based session with a short token", OperationID: "createKeyBasedSession", Method: http.MethodPost, Path: "/__admin/sessions/key-based", Body: `{"tokens":["AB12"]}`, InvalidRequest: true, WantStatus: statuses(422), WantError: "token_too_short"},
	{Name: "create key-based session with a token in use", OperationID: "createKeyBasedSession", Method: http.MethodPost, Path: "/__admin/sessions/key-based", Body: `{"tokens":["CONTRACT-FIXTURE-{runId}"]}`, WantStatus: statuses(409), WantError: "token_in_use", Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "create URL-based session", OperationID: "createURLBasedSession", Method: http.MethodPost, Path: "/__admin/sessions/url-based", Body: `{"name":"contract-ci","ttlSeconds":3600}`, WantStatus: statuses(201)},
	{Name: "create URL-based session with too long a TTL", OperationID: "createURLBasedSession", Method: http.MethodPost, Path: "/__admin/sessions/url-based", Body: `{"ttlSeconds":2592001}`, InvalidRequest: true, WantStatus: statuses(422)},
	{Name: "get session", OperationID: "getSession", Method: http.MethodGet, Path: "/__admin/sessions/{sessionId}", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "get unknown session", OperationID: "getSession", Method: http.MethodGet, Path: "/__admin/sessions/" + unknownSessionID, WantStatus: statuses(404)},
	{Name: "patch session", OperationID: "updateSession", Method: http.MethodPatch, Path: "/__admin/sessions/{sessionId}", Body: `{"ttlSeconds":1200,"addTokens":["CONTRACT-TOKEN-2-{runId}"]}`, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "keepalive", OperationID: "keepSessionAlive", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/keepalive", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "delete unknown session", OperationID: "deleteSession", Method: http.MethodDelete, Path: "/__admin/sessions/" + unknownSessionID, WantStatus: statuses(404)},

	// ── Session scope ──
	{Name: "list session mappings", OperationID: "listSessionMappings", Method: http.MethodGet, Path: "/__admin/sessions/{sessionId}/mappings", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "create session mapping with call counts", OperationID: "createSessionMapping", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/mappings", Body: callCountMapping, WantStatus: statuses(201), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "create session mapping with an invalid call rule", OperationID: "createSessionMapping", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/mappings", Body: `{"request":{"urlPath":"/x"},"response":{"status":200},"callResponses":[{"calls":"4-2","response":{"status":500}}]}`, WantStatus: statuses(422), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "create session mapping with scenario fields", OperationID: "createSessionMapping", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/mappings", Body: scenarioMapping, InvalidRequest: true, WantStatus: statuses(422), WantError: "scenarios_not_supported", Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "get session mapping", OperationID: "getSessionMapping", Method: http.MethodGet, Path: "/__admin/sessions/{sessionId}/mappings/{sessionStubMappingId}", WantStatus: statuses(200), Needs: []Fixture{FixtureSessionMapping}},
	{Name: "replace session mapping", OperationID: "replaceSessionMapping", Method: http.MethodPut, Path: "/__admin/sessions/{sessionId}/mappings/{sessionStubMappingId}", Body: simpleMapping, WantStatus: statuses(200), Needs: []Fixture{FixtureSessionMapping}},
	{Name: "delete unknown session mapping", OperationID: "deleteSessionMapping", Method: http.MethodDelete, Path: "/__admin/sessions/{sessionId}/mappings/" + unknownMappingID, WantStatus: statuses(404), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "import session mappings", OperationID: "importSessionMappings", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/mappings/import", Body: `{"mappings":[` + simpleMapping + `]}`, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "delete all session mappings", OperationID: "deleteAllSessionMappings", Method: http.MethodDelete, Path: "/__admin/sessions/{urlMockId}/mappings", WantStatus: statuses(200), Needs: []Fixture{FixtureURLBasedSession}},
	{Name: "list session files", OperationID: "listSessionFiles", Method: http.MethodGet, Path: "/__admin/sessions/{sessionId}/files", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "put session file", OperationID: "putSessionFile", Method: http.MethodPut, Path: "/__admin/sessions/{sessionId}/files/contract.json", Body: `{"source":"session"}`, ContentType: "application/octet-stream", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "get session file", OperationID: "getSessionFile", Method: http.MethodGet, Path: "/__admin/sessions/{sessionId}/files/{fileId}", WantStatus: statuses(200), Needs: []Fixture{FixtureSessionFile}},
	{Name: "delete session file", OperationID: "deleteSessionFile", Method: http.MethodDelete, Path: "/__admin/sessions/{sessionId}/files/{fileId}", WantStatus: statuses(200), Needs: []Fixture{FixtureSessionFile}},
	{Name: "list session requests", OperationID: "listSessionRequests", Method: http.MethodGet, Path: "/__admin/sessions/{sessionId}/requests", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "clear session requests", OperationID: "clearSessionRequests", Method: http.MethodDelete, Path: "/__admin/sessions/{sessionId}/requests", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "count session requests", OperationID: "countSessionRequests", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/requests/count", Body: requestPattern, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "find session requests", OperationID: "findSessionRequests", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/requests/find", Body: requestPattern, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "remove session requests", OperationID: "removeSessionRequests", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/requests/remove", Body: requestPattern, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "session unmatched requests", OperationID: "listSessionUnmatchedRequests", Method: http.MethodGet, Path: "/__admin/sessions/{sessionId}/requests/unmatched", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "session near misses for unmatched", OperationID: "sessionNearMissesForUnmatched", Method: http.MethodGet, Path: "/__admin/sessions/{sessionId}/requests/unmatched/near-misses", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "export session requests (SaaS only)", OperationID: "exportSessionRequests", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/requests/export", WantStatus: statuses(202, 404), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "session near misses for a request", OperationID: "sessionNearMissesForRequest", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/near-misses/request", Body: sampleRequest, WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "session counters", OperationID: "listSessionCounters", Method: http.MethodGet, Path: "/__admin/sessions/{sessionId}/counters", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "reset session counters", OperationID: "resetSessionCounters", Method: http.MethodPost, Path: "/__admin/sessions/{sessionId}/counters/reset", WantStatus: statuses(200), Needs: []Fixture{FixtureKeyBasedSession}},
	{Name: "set a session counter", OperationID: "setSessionCounter", Method: http.MethodPut, Path: "/__admin/sessions/{sessionId}/counters/{sessionStubMappingId}", Body: `{"count":2}`, WantStatus: statuses(200), Needs: []Fixture{FixtureSessionMapping}},
	{Name: "reset a session counter", OperationID: "resetSessionCounter", Method: http.MethodDelete, Path: "/__admin/sessions/{sessionId}/counters/{sessionStubMappingId}", WantStatus: statuses(200), Needs: []Fixture{FixtureSessionMapping}},
	{Name: "start session recording", OperationID: "startSessionRecording", Method: http.MethodPost, Path: "/__admin/sessions/{urlMockId}/recordings/start", Body: `{"targetBaseUrl":"http://localhost:8089","repeatsAsCallResponses":true}`, WantStatus: statuses(200), Needs: []Fixture{FixtureURLBasedSession}},
	{Name: "session recording status", OperationID: "getSessionRecordingStatus", Method: http.MethodGet, Path: "/__admin/sessions/{urlMockId}/recordings/status", WantStatus: statuses(200), Needs: []Fixture{FixtureURLBasedSession}},
	{Name: "stop session recording", OperationID: "stopSessionRecording", Method: http.MethodPost, Path: "/__admin/sessions/{urlMockId}/recordings/stop", WantStatus: statuses(200), Needs: []Fixture{FixtureURLBasedSession}},

	// ── Runtime and platform ──
	{Name: "live stats", OperationID: "getLiveStats", Method: http.MethodGet, Path: "/__admin/stats/live?window=15m", WantStatus: statuses(200)},
	{Name: "usage (SaaS only)", OperationID: "getUsage", Method: http.MethodGet, Path: "/__admin/usage", WantStatus: statuses(200, 404)},
	{Name: "liveness", OperationID: "getLiveness", Method: http.MethodGet, Path: "/__health", WantStatus: statuses(200)},
	{Name: "readiness", OperationID: "getReadiness", Method: http.MethodGet, Path: "/__ready", WantStatus: statuses(200, 503)},
	{Name: "metrics", OperationID: "getMetrics", Method: http.MethodGet, Path: "/__metrics", WantStatus: statuses(200, 404)},

	// ── Destructive cases run last ──
	{Name: "delete session", OperationID: "deleteSession", Method: http.MethodDelete, Path: "/__admin/sessions/{urlMockId}", WantStatus: statuses(200), Needs: []Fixture{FixtureURLBasedSession}},
	{Name: "shutdown (standalone only, never sent)", OperationID: "shutdown", Method: http.MethodPost, Path: "/__admin/shutdown", WantStatus: statuses(200, 404)},
}

// SkipLive reports whether a case must not be sent to a live engine, because it would stop
// or wipe a shared environment. Its operation still counts as covered.
func (c Case) SkipLive() bool {
	return c.OperationID == "shutdown" || c.Header[destructiveHeader] != ""
}

// destructiveHeader marks a case as unsafe for shared environments. It is never sent.
const destructiveHeader = "X-Contract-Destructive"
