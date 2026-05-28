package observe

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OTelSink exports each event as an OTLP/HTTP span so Spout runs show up in an
// existing observability stack (Datadog, Grafana Tempo, Honeycomb, …). It speaks
// OTLP/HTTP+JSON directly — no SDK dependency, one POST per event. The run's
// trace id is derived from its name, so every event shares one trace.
//
// Best-effort, like ServerSink: the local archive is canonical, so export
// failures are swallowed.
type OTelSink struct {
	endpoint string // .../v1/traces
	traceID  string // 16-byte hex (32 chars), stable per run
	http     *http.Client
}

// NewOTelSink targets an OTLP/HTTP collector base URL (e.g. http://collector:4318).
func NewOTelSink(endpoint, run string) *OTelSink {
	base := strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(base, "/v1/traces") {
		base += "/v1/traces"
	}
	sum := sha256.Sum256([]byte(run))
	return &OTelSink{
		endpoint: base,
		traceID:  hex.EncodeToString(sum[:16]),
		http:     &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *OTelSink) Emit(e Event) {
	body, err := json.Marshal(s.payload(e))
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := s.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

func (s *OTelSink) Close() {}

// --- minimal OTLP/HTTP JSON shapes ---

type otlpAttr struct {
	Key   string       `json:"key"`
	Value otlpAttrVal  `json:"value"`
}
type otlpAttrVal struct {
	StringValue string `json:"stringValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

func (s *OTelSink) payload(e Event) map[string]any {
	ts := strconv.FormatInt(e.TS.UnixNano(), 10)
	span := map[string]any{
		"traceId":           s.traceID,
		"spanId":            spanID(e.ID),
		"name":              "spout." + e.Kind,
		"kind":              1, // SPAN_KIND_INTERNAL
		"startTimeUnixNano": ts,
		"endTimeUnixNano":   ts,
		"attributes":        eventAttrs(e),
	}
	return map[string]any{
		"resourceSpans": []any{map[string]any{
			"resource": map[string]any{
				"attributes": []otlpAttr{strAttr("service.name", "spout")},
			},
			"scopeSpans": []any{map[string]any{
				"scope": map[string]any{"name": "spout/observe"},
				"spans": []any{span},
			}},
		}},
	}
}

func eventAttrs(e Event) []otlpAttr {
	attrs := []otlpAttr{
		strAttr("spout.run", e.Run),
		strAttr("spout.trigger", e.Trigger),
	}
	if o := e.Obs; o != nil {
		attrs = append(attrs,
			strAttr("spout.status", o.Status),
			strAttr("spout.severity", o.Severity),
			strAttr("spout.summary", o.Summary),
		)
		if o.RunType != "" {
			attrs = append(attrs, strAttr("spout.run_type", o.RunType))
		}
		for name, evidence := range o.Detectors {
			if evidence != "" {
				attrs = append(attrs, strAttr("spout.detector."+name, evidence))
			}
		}
		for _, m := range o.Metrics {
			v := m.Value
			attrs = append(attrs, otlpAttr{Key: "spout.metric." + m.Name, Value: otlpAttrVal{DoubleValue: &v}})
		}
	}
	return attrs
}

func strAttr(k, v string) otlpAttr {
	return otlpAttr{Key: k, Value: otlpAttrVal{StringValue: v}}
}

// spanID derives a stable 8-byte hex span id from an event id.
func spanID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%x", sum[:8])
}
