package admin

import (
	"encoding/base64"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pwgith/shadow-mock/internal/engine"
	"github.com/pwgith/shadow-mock/internal/journal"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/mock"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
)

type metaView struct {
	Total int `json:"total"`
}

type mappingList struct {
	Mappings []model.StubMapping `json:"mappings"`
	Meta     metaView            `json:"meta"`
}

type queryParam struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// loggedRequest is WireMock's LoggedRequest.
type loggedRequest struct {
	ID               string                `json:"id,omitempty"`
	URL              string                `json:"url"`
	AbsoluteURL      string                `json:"absoluteUrl,omitempty"`
	Method           string                `json:"method"`
	ClientIP         string                `json:"clientIp,omitempty"`
	Headers          map[string]any        `json:"headers"`
	Cookies          map[string]any        `json:"cookies"`
	QueryParams      map[string]queryParam `json:"queryParams"`
	Body             string                `json:"body"`
	BodyAsBase64     string                `json:"bodyAsBase64"`
	Protocol         string                `json:"protocol"`
	Scheme           string                `json:"scheme"`
	Host             string                `json:"host"`
	Port             int                   `json:"port"`
	FormParams       map[string]queryParam `json:"formParams"`
	BrowserProxy     bool                  `json:"browserProxyRequest"`
	LoggedDate       int64                 `json:"loggedDate,omitempty"`
	LoggedDateString string                `json:"loggedDateString,omitempty"`
}

func headerMap(h http.Header) map[string]any {
	out := make(map[string]any, len(h))
	for k, vs := range h {
		if len(vs) == 1 {
			out[k] = vs[0]
		} else {
			out[k] = vs
		}
	}
	return out
}

func requestView(r *match.Request, e *journal.Entry) loggedRequest {
	v := loggedRequest{
		URL: r.URL(), Method: r.Method, ClientIP: r.ClientIP, Headers: headerMap(r.Header),
		Cookies: map[string]any{}, QueryParams: map[string]queryParam{},
	}
	for k, vs := range r.Cookies {
		if len(vs) == 1 {
			v.Cookies[k] = vs[0]
		} else {
			v.Cookies[k] = vs
		}
	}
	for k, vs := range r.Query {
		v.QueryParams[k] = queryParam{Key: k, Values: vs}
	}
	v.Body = bodyText(r.Body)
	v.BodyAsBase64 = base64.StdEncoding.EncodeToString(r.Body)
	v.Protocol, v.Scheme, v.Host, v.Port = "HTTP/1.1", r.Scheme, r.Host, r.Port
	v.FormParams = map[string]queryParam{}
	for k, vs := range r.Form() {
		v.FormParams[k] = queryParam{Key: k, Values: vs}
	}
	if e != nil {
		v.ID, v.AbsoluteURL = e.ID, e.AbsoluteURL
		v.LoggedDate = e.LoggedAt.UnixMilli()
		v.LoggedDateString = e.LoggedAt.UTC().Format(time.RFC3339Nano)
	}
	return v
}

// bodyText returns the body as text, or nothing when it is not valid UTF-8; the base64 field holds
// such a body exactly.
func bodyText(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return ""
}

type responseView struct {
	Status       int            `json:"status"`
	Headers      map[string]any `json:"headers,omitempty"`
	Body         string         `json:"body"`
	BodyAsBase64 string         `json:"bodyAsBase64"`
}

type timingView struct {
	AddedDelay       int64 `json:"addedDelay"`
	ProcessTime      int64 `json:"processTime"`
	ResponseSendTime int64 `json:"responseSendTime"`
	ServeTime        int64 `json:"serveTime"`
	TotalTime        int64 `json:"totalTime"`
}

type detectionView struct {
	Stage               string   `json:"stage,omitempty"`
	Offset              int      `json:"offset"`
	ScanTruncated       bool     `json:"scanTruncated,omitempty"`
	AmbiguousSessionIDs []string `json:"ambiguousSessionIds,omitempty"`
}

type shadowView struct {
	Tenant     string         `json:"tenant"`
	SessionID  *string        `json:"sessionId"`
	Layer      string         `json:"layer"`
	Detection  *detectionView `json:"detection,omitempty"`
	CallNumber int64          `json:"callNumber,omitempty"`
}

type serveEvent struct {
	ID                 string                    `json:"id"`
	Request            loggedRequest             `json:"request"`
	StubMapping        *model.StubMapping        `json:"stubMapping,omitempty"`
	ResponseDefinition *model.ResponseDefinition `json:"responseDefinition,omitempty"`
	Response           responseView              `json:"response"`
	WasMatched         bool                      `json:"wasMatched"`
	Timing             timingView                `json:"timing"`
	ShadowMock         shadowView                `json:"shadowMock"`
}

// layerOf returns the layer that served an entry, or for an unmatched entry the most
// specific layer it was resolved against.
func layerOf(e *journal.Entry) string {
	if e.Layer != "" {
		return e.Layer
	}
	switch {
	case e.SessionID == "":
		return string(session.Global)
	case e.Detection != nil && e.Detection.Stage != "" && e.SessionID == firstOr(e.Sessions):
		return string(session.KeyBased)
	default:
		return string(session.URLBased)
	}
}

func firstOr(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[0]
}

func eventView(e *journal.Entry) serveEvent {
	v := serveEvent{
		ID: e.ID, Request: requestView(e.Request, e), StubMapping: e.Stub, ResponseDefinition: e.Definition,
		WasMatched: e.Matched(),
		Response:   responseView{Status: e.Response.Status, Headers: headerMap(e.Response.Header)},
		Timing:     timingView{AddedDelay: e.AddedDelay, ProcessTime: e.ProcessTime, ServeTime: e.ProcessTime, TotalTime: e.ProcessTime + e.AddedDelay},
		ShadowMock: shadowView{Tenant: string(e.Tenant), Layer: layerOf(e), CallNumber: e.CallNumber},
	}
	v.Response.Body = bodyText(e.Response.Body)
	v.Response.BodyAsBase64 = base64.StdEncoding.EncodeToString(e.Response.Body)
	if e.SessionID != "" {
		id := e.SessionID
		v.ShadowMock.SessionID = &id
	}
	if d := e.Detection; d != nil && (d.Stage != "" || d.ScanTruncated) {
		v.ShadowMock.Detection = &detectionView{Stage: d.Stage, Offset: d.Offset, ScanTruncated: d.ScanTruncated, AmbiguousSessionIDs: d.Ambiguous}
	}
	return v
}

type serveEventList struct {
	Requests               []serveEvent `json:"requests"`
	Meta                   metaView     `json:"meta"`
	RequestJournalDisabled bool         `json:"requestJournalDisabled"`
}

type findResult struct {
	Requests []loggedRequest `json:"requests"`
}

func findView(es []*journal.Entry) findResult {
	out := findResult{Requests: make([]loggedRequest, 0, len(es))}
	for _, e := range es {
		out.Requests = append(out.Requests, requestView(e.Request, e))
	}
	return out
}

type matchResult struct {
	Distance float64 `json:"distance"`
}

type nearMissView struct {
	Request        *loggedRequest        `json:"request,omitempty"`
	RequestPattern *model.RequestPattern `json:"requestPattern,omitempty"`
	StubMapping    *model.StubMapping    `json:"stubMapping,omitempty"`
	Layer          string                `json:"layer,omitempty"`
	MatchResult    matchResult           `json:"matchResult"`
}

type nearMissList struct {
	NearMisses []nearMissView `json:"nearMisses"`
}

func stubNearMisses(req *match.Request, nms []engine.NearMiss, e *journal.Entry) []nearMissView {
	out := make([]nearMissView, 0, len(nms))
	lr := requestView(req, e)
	for _, nm := range nms {
		m := nm.Stub.Mapping
		out = append(out, nearMissView{
			Request: &lr, StubMapping: &m, Layer: string(nm.Layer.Kind),
			MatchResult: matchResult{Distance: round(nm.Distance)},
		})
	}
	return out
}

func round(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }

type counterView struct {
	StubMappingID string     `json:"stubMappingId"`
	StubLayer     string     `json:"stubLayer"`
	Count         int64      `json:"count"`
	LastCalledAt  *time.Time `json:"lastCalledAt,omitempty"`
}

type counterList struct {
	Counters []counterView `json:"counters"`
}

func toCounterView(c mock.CounterView) counterView {
	v := counterView{StubMappingID: c.StubID, StubLayer: string(c.Layer), Count: c.Count}
	if !c.LastCalledAt.IsZero() {
		t := c.LastCalledAt
		v.LastCalledAt = &t
	}
	return v
}

// sessionView is the Session schema.
type sessionView struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	Name         string         `json:"name,omitempty"`
	Tenant       string         `json:"tenant"`
	CreatedBy    string         `json:"createdBy"`
	CreatedAt    time.Time      `json:"createdAt"`
	ExpiresAt    time.Time      `json:"expiresAt"`
	TTLSeconds   int64          `json:"ttlSeconds"`
	ParentID     *string        `json:"parentId"`
	KeyPrefix    string         `json:"keyPrefix,omitempty"`
	Tokens       []string       `json:"tokens,omitempty"`
	BaseURL      string         `json:"baseUrl,omitempty"`
	Masks        []model.Mask   `json:"masks,omitempty"`
	MappingCount int            `json:"mappingCount"`
	RequestCount int            `json:"requestCount"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	// Key is set only in the response to creation.
	Key string `json:"key,omitempty"`
}

func baseURLFor(public string, r *http.Request, id string) string {
	base := strings.TrimSuffix(public, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host + basePath(r.Context())
	}
	return base + "/d/" + id
}
