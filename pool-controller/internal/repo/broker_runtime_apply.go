package repo

import (
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

const appliedBrokerRuntimeBucket = "applied_broker_runtime"
const appliedBrokerRuntimeKey = "current"

func (r *StateRepo) PutAppliedBrokerRuntime(runtime types.AppliedBrokerRuntime) error {
	if runtime.AppliedRevision == "" {
		return fmt.Errorf("applied broker runtime revision is required")
	}
	return putJSON(r, appliedBrokerRuntimeBucket, appliedBrokerRuntimeKey, runtime)
}

func (r *StateRepo) GetAppliedBrokerRuntime() (types.AppliedBrokerRuntime, error) {
	var out types.AppliedBrokerRuntime
	err := getJSON(r, appliedBrokerRuntimeBucket, appliedBrokerRuntimeKey, &out)
	return out, err
}
