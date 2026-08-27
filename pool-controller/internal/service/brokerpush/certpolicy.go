package brokerpush

import (
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
)

// Certification policy (plan 0043 §3.5, decision 6b).
//
// These are the families that used to be hardcoded Go in
// `internal/service/probes` and run BY the controller against a member's
// backend URL. They are now step CONFIG the controller pushes onto the
// offer, and the broker executes them over the runner's attach
// connection — which is the only path that still exists once members
// stop exposing URLs at all.
//
// Two things changed besides the location. The steps are data, so a
// template can carry its own without a controller release. And the
// per-capability guesswork is gone: `strings.Contains(capability_id,
// "transcription")` picking a multipart body was a heuristic standing in
// for a fact the runner now declares.
//
// These mirror certification-steps.md §7, so a runner built against the
// agent's adapter profiles passes the steps a pool ships.

func boolPtr(b bool) *bool { return &b }

// CertificationPolicy returns the steps an offer for this capability
// should require. model, when known, is substituted into request bodies
// — though the broker's own {{identity.openai.model}} substitution is
// preferred where the runner declares it, so the step works for every
// runner the offer matches rather than one.
func CertificationPolicy(capabilityID, model string) []brokeradmin.OfferPushCertStep {
	ready := brokeradmin.OfferPushCertStep{Name: "ready", Type: "readiness"}
	usage := brokeradmin.OfferPushCertStep{Name: "usage", Type: "usage", Config: map[string]any{"min_units": 1}}

	switch strings.TrimSpace(capabilityID) {
	case "openai:chat-completions":
		return []brokeradmin.OfferPushCertStep{ready, {
			Name: "smoke", Type: "request",
			Config: map[string]any{
				"transport": "unary",
				"body": map[string]any{
					"model":      "{{identity.openai.model}}",
					"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
					"max_tokens": 8,
				},
				"assert": []any{
					"$.choices[0].message.content",
					map[string]any{"path": "$.usage.total_tokens", "min": 1},
				},
			},
		}, usage, {
			Name: "latency", Type: "latency", Required: boolPtr(false),
			Config: map[string]any{"samples": 3, "p50_max_ms": 4000, "measure": "first_byte"},
		}}

	case "openai:embeddings":
		return []brokeradmin.OfferPushCertStep{ready, {
			Name: "smoke", Type: "request",
			Config: map[string]any{
				"body":   map[string]any{"model": "{{identity.openai.model}}", "input": "ping"},
				"assert": []any{map[string]any{"path": "$.data[0].embedding", "min": 1}, "$.usage.total_tokens"},
			},
		}, usage}

	case "openai:audio-transcriptions", "openai:audio-translations":
		// Multipart, and metered by input duration — the two facts the
		// old capability-id heuristic existed to guess.
		return []brokeradmin.OfferPushCertStep{
			{Name: "ready", Type: "readiness", Config: map[string]any{"attempts": 5, "interval_ms": 3000}},
			{
				Name: "smoke", Type: "request",
				Config: map[string]any{
					"transport": "multipart",
					"parts": []any{
						map[string]any{"name": "model", "value": "{{identity.openai.model}}"},
						map[string]any{
							"name": "file", "filename": "probe.wav", "content_type": "audio/wav",
							"fixture": map[string]any{"ref": "multipart-audio-duration-v1/wav-16k-mono-3s"},
						},
					},
					"assert": []any{"$.text"},
				},
			},
			usage,
		}

	case "openai:audio-speech":
		// JSON in, audio bytes out: there is no usage block to read, so
		// the response is checked by content type.
		return []brokeradmin.OfferPushCertStep{ready, {
			Name: "smoke", Type: "request",
			Config: map[string]any{
				"body":                map[string]any{"model": "{{identity.openai.model}}", "input": "ping"},
				"expect_content_type": "audio/",
				"max_response_bytes":  4194304,
			},
		}, usage}

	case "openai:images-generations":
		return []brokeradmin.OfferPushCertStep{ready, {
			Name: "smoke", Type: "request",
			Config: map[string]any{
				"body":   map[string]any{"model": "{{identity.openai.model}}", "prompt": "a probe", "n": 1},
				"assert": []any{map[string]any{"path": "$.data[0]", "min": 0}},
			},
		}, usage}

	case "video:transcode.abr", "video:transcode.vod":
		return []brokeradmin.OfferPushCertStep{ready, {
			Name: "smoke", Type: "request", TimeoutMS: 60000,
			Config: map[string]any{
				"transport": "multipart",
				"parts": []any{
					map[string]any{"name": "profiles", "value": `[{"name":"720p30","width":1280,"height":720,"fps":30}]`},
					map[string]any{
						"name": "source", "filename": "probe.mp4", "content_type": "video/mp4",
						"fixture": map[string]any{"ref": "video/mp4-2s-720p"},
					},
				},
				"expect_content_type": "video/",
			},
		}, usage, {
			Name: "latency", Type: "latency", Required: boolPtr(false),
			Config: map[string]any{"samples": 2, "p50_max_ms": 20000},
		}}

	default:
		// An unknown capability still gets readiness: proving a runner
		// answers at all is better than certifying on match, and it is
		// the one step that needs no workload knowledge. A pool that
		// wants more ships a template with its own steps.
		return []brokeradmin.OfferPushCertStep{ready}
	}
}
