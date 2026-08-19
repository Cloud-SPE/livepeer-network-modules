package server

import (
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/bytescounted"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/ffmpegprogress"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/openaiusage"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/requestformula"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/responseheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/responsejsonpath"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/responsetrailer"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors/secondselapsed"
)

// defaultExtractors returns the registry pre-populated with the v0.1
// extractor set.
func defaultExtractors() *extractors.Registry {
	r := extractors.NewRegistry()
	r.Register(responsejsonpath.Name, responsejsonpath.New) // plan 0003
	r.Register(responseheader.Name, responseheader.New)     // audio response-header extraction
	r.Register(responsetrailer.Name, responsetrailer.New)   // streaming work-units via HTTP trailer
	r.Register(openaiusage.Name, openaiusage.New)           // plan 0007
	r.Register(requestformula.Name, requestformula.New)     // plan 0007
	r.Register(bytescounted.Name, bytescounted.New)         // plan 0007
	r.Register(secondselapsed.Name, secondselapsed.New)     // plan 0007
	r.Register(ffmpegprogress.Name, ffmpegprogress.New)     // plan 0007
	return r
}
