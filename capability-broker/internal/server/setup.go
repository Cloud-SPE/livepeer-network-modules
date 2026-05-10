package server

import (
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/bytescounted"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/ffmpegprogress"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/openaiusage"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/requestformula"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/responseheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/responsejsonpath"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/runnerreport"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/secondselapsed"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/httpmultipart"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/httpreqresp"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/httpstream"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/rtmpingresshlsegress"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/sessioncontrolplusmedia"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/wsrealtime"
)

// defaultModes returns the registry pre-populated with the v0.1 driver
// set. The rtmp-ingress driver is constructed by the caller because it
// holds session state shared with the broker's RTMP listener.
func defaultModes(rtmpDriver *rtmpingresshlsegress.Driver, sessDriver *sessioncontrolplusmedia.Driver) *modes.Registry {
	r := modes.NewRegistry()
	r.Register(httpreqresp.New())   // plan 0003
	r.Register(httpstream.New())    // plan 0006
	r.Register(httpmultipart.New()) // plan 0006
	r.Register(wsrealtime.New())    // plan 0010
	r.Register(rtmpDriver)          // plan 0011-followup
	r.Register(sessDriver)          // plan 0012 + 0012-followup
	return r
}

// defaultExtractors returns the registry pre-populated with the v0.1
// extractor set. All six spec-defined extractors are registered.
func defaultExtractors() *extractors.Registry {
	r := extractors.NewRegistry()
	r.Register(responsejsonpath.Name, responsejsonpath.New) // plan 0003
	r.Register(responseheader.Name, responseheader.New)     // audio response-header extraction
	r.Register(openaiusage.Name, openaiusage.New)           // plan 0007
	r.Register(requestformula.Name, requestformula.New)     // plan 0007
	r.Register(bytescounted.Name, bytescounted.New)         // plan 0007
	r.Register(secondselapsed.Name, secondselapsed.New)     // plan 0007
	r.Register(ffmpegprogress.Name, ffmpegprogress.New)     // plan 0007
	r.Register(runnerreport.Name, runnerreport.New)         // plan 0012-followup
	return r
}
