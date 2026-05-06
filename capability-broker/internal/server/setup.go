package server

import (
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/responsejsonpath"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/httpmultipart"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/httpreqresp"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/httpstream"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/rtmpingresshlsegress"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/sessioncontrolplusmedia"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/wsrealtime"
)

// defaultModes returns the registry pre-populated with the v0.1 driver set.
//
// All six spec modes are registered. The streaming modes (rtmp-ingress
// and session-control-plus-media) implement the session-open phase only
// in v0.1; their full media-plane integration is queued as future work.
func defaultModes() *modes.Registry {
	r := modes.NewRegistry()
	r.Register(httpreqresp.New())              // plan 0003
	r.Register(httpstream.New())               // plan 0006
	r.Register(httpmultipart.New())            // plan 0006
	r.Register(wsrealtime.New())               // plan 0010
	r.Register(rtmpingresshlsegress.New())     // plan 0011 (session-open phase)
	r.Register(sessioncontrolplusmedia.New())  // plan 0012 (session-open phase)
	return r
}

// defaultExtractors returns the registry pre-populated with the v0.1
// extractor set:
//   - response-jsonpath
//
// Other extractors (openai-usage, request-formula, bytes-counted,
// seconds-elapsed, ffmpeg-progress) are not yet implemented; their
// factories ship in plan 0007.
func defaultExtractors() *extractors.Registry {
	r := extractors.NewRegistry()
	r.Register(responsejsonpath.Name, responsejsonpath.New)
	return r
}
