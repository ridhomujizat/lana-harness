package agent

import "github.com/ridhomujizat/lana-harness/core"

// SetQuestions installs the question service used by turns. It is set at boot,
// before any turn starts.
func (r *Registry) SetQuestions(service core.QuestionService) {
	if r == nil {
		return
	}
	r.questions = service
}
