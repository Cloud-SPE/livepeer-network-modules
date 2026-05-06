package server

import (
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/responsejsonpath"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/httpreqresp"
)

// defaultModes returns the registry pre-populated with the v0.1 driver set:
//   - http-reqresp@v0
//
// Other modes (http-stream, http-multipart, ws-realtime,
// rtmp-ingress-hls-egress, session-control-plus-media) are not yet
// implemented; their drivers ship in plan 0006.
func defaultModes() *modes.Registry {
	r := modes.NewRegistry()
	r.Register(httpreqresp.New())
	return r
}

// defaultExtractors returns the registry pre-populated with the v0.1
// extractor set:
//   - response-jsonpath
//
// Other extractors (openai-usage, request-formula, bytes-counted,
// seconds-elapsed, ffmpeg-progress) are not yet implemented; their factories
// ship in plan 0007.
func defaultExtractors() *extractors.Registry {
	r := extractors.NewRegistry()
	r.Register(responsejsonpath.Name, responsejsonpath.New)
	return r
}
