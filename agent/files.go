package agent

import "github.com/ridhomujizat/lana-harness/core"

// SetFiles installs the file service the send_file tool uses. It is set at
// boot, before any turn starts.
func (r *Registry) SetFiles(service core.FileSender) {
	if r == nil {
		return
	}
	r.files = service
}
