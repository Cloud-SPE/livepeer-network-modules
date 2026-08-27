package attach

import (
	"fmt"
	"sort"
	"strings"
)

// Adapter profiles (plan 0043 item 12).
//
// A profile is the translation from "I run this container" to the
// capability entry the contract wants: the endpoint it serves, the
// transports it speaks, the unit it meters in and the extractor that
// counts it, its readiness recipe, and the identity an offer selects
// on. The operator supplies the model and the URL; the profile supplies
// the eight facts they should never have to know.
//
// Profiles are agent-local. Adding one is an agent change, not a
// protocol change: the contract only cares that the resulting entry is
// valid.

const (
	ProfileOpenAICompatible = "openai-compatible"
	ProfileTranscode        = "transcode"
)

// Profiles lists the supported profile names, sorted.
func Profiles() []string { return []string{ProfileOpenAICompatible, ProfileTranscode} }

// openaiFamily is one endpoint of an OpenAI-compatible server.
type openaiFamily struct {
	path       string
	transports []string
	workUnit   string
	extractor  map[string]any
}

// openaiFamilies maps a capability id to what that endpoint actually
// looks like on the wire. These mirror the certification families in
// livepeer-network-protocol/protocols/certification-steps.md §7, so a
// runner declared here passes the steps a template ships.
var openaiFamilies = map[string]openaiFamily{
	"openai:chat-completions": {
		path: "/v1/chat/completions", transports: []string{"unary", "stream"},
		workUnit: "tokens", extractor: map[string]any{"type": "openai-usage", "field": "total_tokens"},
	},
	"openai:embeddings": {
		path: "/v1/embeddings", transports: []string{"unary"},
		workUnit: "tokens", extractor: map[string]any{"type": "openai-usage", "field": "total_tokens"},
	},
	"openai:audio-transcriptions": {
		path: "/v1/audio/transcriptions", transports: []string{"multipart"},
		workUnit: "seconds", extractor: map[string]any{"type": "multipart-audio-duration", "file_field": "file", "unit": "seconds"},
	},
	"openai:audio-translations": {
		path: "/v1/audio/translations", transports: []string{"multipart"},
		workUnit: "seconds", extractor: map[string]any{"type": "multipart-audio-duration", "file_field": "file", "unit": "seconds"},
	},
	"openai:audio-speech": {
		path: "/v1/audio/speech", transports: []string{"unary"},
		// Speech bills on what the caller sent: the response is audio
		// bytes, so there is no usage block to read.
		workUnit: "characters", extractor: map[string]any{
			"type": "request-formula", "expression": "len_input",
			"fields": map[string]any{"len_input": "$.input"}, "default": 1,
		},
	},
	"openai:images-generations": {
		path: "/v1/images/generations", transports: []string{"unary"},
		workUnit: "images", extractor: map[string]any{
			"type": "request-formula", "expression": "n",
			"fields": map[string]any{"n": "$.n"}, "default": 1,
		},
	},
}

// OpenAICapabilities lists the capability ids the openai-compatible
// profile knows, sorted.
func OpenAICapabilities() []string {
	out := make([]string, 0, len(openaiFamilies))
	for id := range openaiFamilies {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// expand turns one runner declaration into a capability entry.
func expand(r Runner) (*Capability, error) {
	switch r.Profile {
	case ProfileOpenAICompatible:
		return expandOpenAI(r)
	case ProfileTranscode:
		return expandTranscode(r)
	case "":
		return nil, fmt.Errorf("profile is required (one of %s)", strings.Join(Profiles(), ", "))
	default:
		return nil, fmt.Errorf("unknown profile %q (one of %s)", r.Profile, strings.Join(Profiles(), ", "))
	}
}

func expandOpenAI(r Runner) (*Capability, error) {
	capID := r.CapabilityID
	if capID == "" {
		capID = "openai:chat-completions"
	}
	fam, ok := openaiFamilies[capID]
	if !ok {
		return nil, fmt.Errorf("capability_id %q is not an openai-compatible endpoint (one of %s)",
			capID, strings.Join(OpenAICapabilities(), ", "))
	}
	if r.Model == "" {
		return nil, fmt.Errorf("model is required: it is what an offer's match selects on")
	}
	identity := map[string]string{"openai.model": r.Model}
	if r.Provider != "" {
		identity["provider"] = r.Provider
	}
	return &Capability{
		CapabilityID: capID,
		Protocol:     "paid-job/v1",
		LocalID:      r.LocalID,
		Transports:   fam.transports,
		WorkUnit:     WorkUnit{Name: fam.workUnit, Extractor: cloneAny(fam.extractor)},
		Paths:        map[string]string{"invoke": fam.path},
		// The server knows whether the model finished loading; a bare
		// HTTP 200 on /v1/models does not.
		Readiness: Readiness{
			Type: "http-openai-model-ready", Path: "/v1/models",
			Config: map[string]any{"model": r.Model},
		},
		Identity:       identity,
		SchemaVersions: map[string]string{"paid-job/v1": "1.0.15"},
		Requirements:   r.Requirements,
		Extensions:     r.Extensions,
	}, nil
}

func expandTranscode(r Runner) (*Capability, error) {
	capID := r.CapabilityID
	if capID == "" {
		capID = "video:transcode.abr"
	}
	identity := map[string]string{}
	if r.Model != "" {
		// Transcoders have no model; the field carries the codec when set.
		identity["codec"] = r.Model
	}
	if r.Provider != "" {
		identity["provider"] = r.Provider
	}
	return &Capability{
		CapabilityID: capID,
		Protocol:     "paid-job/v1",
		LocalID:      r.LocalID,
		Transports:   []string{"multipart"},
		WorkUnit: WorkUnit{
			Name:      "output_seconds",
			Extractor: map[string]any{"type": "ffmpeg-progress", "unit": "out_time_seconds"},
		},
		Paths:          map[string]string{"invoke": "/transcode"},
		Readiness:      Readiness{Type: "http-status", Path: "/healthz"},
		Identity:       identity,
		SchemaVersions: map[string]string{"paid-job/v1": "1.0.15"},
		Requirements:   r.Requirements,
		Extensions:     r.Extensions,
	}, nil
}

func cloneAny(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if nested, ok := v.(map[string]any); ok {
			out[k] = cloneAny(nested)
			continue
		}
		out[k] = v
	}
	return out
}
