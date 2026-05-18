package repo

import (
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

const desiredBrokerRuntimeBucket = "desired_broker_runtime"
const desiredBrokerRuntimeKey = "current"

func (r *StateRepo) PutDesiredBrokerRuntime(runtime types.DesiredBrokerRuntime) error {
	if runtime.Revision == "" {
		return fmt.Errorf("desired broker runtime revision is required")
	}
	return putJSON(r, desiredBrokerRuntimeBucket, desiredBrokerRuntimeKey, runtime)
}

func (r *StateRepo) GetDesiredBrokerRuntime() (types.DesiredBrokerRuntime, error) {
	var out types.DesiredBrokerRuntime
	err := getJSON(r, desiredBrokerRuntimeBucket, desiredBrokerRuntimeKey, &out)
	return out, err
}
