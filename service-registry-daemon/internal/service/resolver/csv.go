package resolver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// csvManifest is the shape we accept for the read-only CSV-fallback
// mode. Loosely follows the rejected on-chain proposal's payload
// structure but with full-tolerance: missing fields are permitted.
type csvManifest struct {
	Version int       `json:"version"`
	Nodes   []csvNode `json:"nodes"`
}

type csvNode struct {
	IP              string `json:"ip"`
	Port            int    `json:"port"`
	URL             string `json:"url"` // some publishers include url instead of ip+port
	CapabilitiesURL string `json:"capabilitiesUrl"`
}

// decodeCSV parses a "<url>,<version>,<base64-json>" serviceURI into
// ResolvedNode entries. Returns ErrUnknownMode if the shape doesn't
// match. Returns synthesized nodes with SignatureStatus=SigUnsigned
// because CSV manifests are not signed.
func decodeCSV(addr types.EthAddress, serviceURI string) (defaultURL string, nodes []types.ResolvedNode, err error) {
	parts := strings.SplitN(serviceURI, ",", 3)
	if len(parts) != 3 {
		return "", nil, fmt.Errorf("%w: csv shape", types.ErrUnknownMode)
	}
	defaultURL = parts[0]

	raw, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		// also tolerate URL-safe / no-padding flavors
		raw, err = base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			return defaultURL, nil, fmt.Errorf("%w: csv base64: %w", types.ErrParse, err)
		}
	}
	var m csvManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return defaultURL, nil, fmt.Errorf("%w: csv inner json: %w", types.ErrParse, err)
	}
	for i, cn := range m.Nodes {
		nodeURL := cn.URL
		if nodeURL == "" && cn.IP != "" && cn.Port > 0 {
			nodeURL = fmt.Sprintf("https://%s:%d", cn.IP, cn.Port)
		}
		if nodeURL == "" {
			continue
		}
		nodes = append(nodes, types.ResolvedNode{
			ID:              fmt.Sprintf("csv-%d", i),
			URL:             nodeURL,
			Source:          types.SourceCSVFallback,
			SignatureStatus: types.SigUnsigned,
			OperatorAddr:    addr,
			Enabled:         true,
			Weight:          100,
		})
	}
	return defaultURL, nodes, nil
}
