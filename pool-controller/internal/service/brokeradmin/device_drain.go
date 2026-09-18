package brokeradmin

import (
	"context"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"net/http"
)

func (c *Client) DrainDevice(ctx context.Context, req ownership.DeviceDrainRequest) (ownership.DeviceDrainProof, error) {
	var proof ownership.DeviceDrainProof
	err := c.doJSON(ctx, http.MethodPost, "/admin/v1/devices/drain", req, &proof)
	return proof, err
}
