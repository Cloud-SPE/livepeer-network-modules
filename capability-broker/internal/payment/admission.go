package payment

import "errors"

// ErrAdmissionStopped is returned only by a local admission fence before the
// receiver request is sent. It proves this new intent has no receiver effects;
// uncertain transport failures must never be classified as this error.
var ErrAdmissionStopped = errors.New("local admission fence refused new work")
