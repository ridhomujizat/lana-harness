package agent

import "github.com/ridhomujizat/lana-harness/core"

// SetSecretRequests installs the secret request service the secret_request tool
// uses (spec 53). It is set at boot, before any turn starts.
func (r *Registry) SetSecretRequests(service core.SecretRequester) {
	if r == nil {
		return
	}
	r.secretReqs = service
}
