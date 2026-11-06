// Package question implements the question service and the input checks shared
// with the ask_user tool.
package question

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
)

// Input limits from API-ASK-001.
const (
	MaxQuestions     = 3
	MaxQuestionRunes = 300
	MaxHeaderRunes   = 30
	MaxOptions       = 6
	MinOptions       = 2
	MaxLabelRunes    = 80
	MaxDescRunes     = 200
)

// Validate checks the questions against the limits of API-ASK-001. It is used
// by the tool and again by the service, so a direct library call cannot skip
// the checks.
func Validate(questions []core.Question) error {
	if len(questions) < 1 || len(questions) > MaxQuestions {
		return fmt.Errorf("questions must have between 1 and %d items", MaxQuestions)
	}
	for i, question := range questions {
		prefix := fmt.Sprintf("questions[%d]", i)
		if strings.TrimSpace(question.Text) == "" {
			return fmt.Errorf("%s.question is required", prefix)
		}
		if utf8.RuneCountInString(question.Text) > MaxQuestionRunes {
			return fmt.Errorf("%s.question must be at most %d characters", prefix, MaxQuestionRunes)
		}
		if utf8.RuneCountInString(question.Header) > MaxHeaderRunes {
			return fmt.Errorf("%s.header must be at most %d characters", prefix, MaxHeaderRunes)
		}
		if len(question.Options) < MinOptions || len(question.Options) > MaxOptions {
			return fmt.Errorf("%s.options must have between %d and %d items", prefix, MinOptions, MaxOptions)
		}
		labels := make(map[string]bool, len(question.Options))
		for j, option := range question.Options {
			optionPrefix := fmt.Sprintf("%s.options[%d]", prefix, j)
			if strings.TrimSpace(option.Label) == "" {
				return fmt.Errorf("%s.label is required", optionPrefix)
			}
			if utf8.RuneCountInString(option.Label) > MaxLabelRunes {
				return fmt.Errorf("%s.label must be at most %d characters", optionPrefix, MaxLabelRunes)
			}
			if labels[option.Label] {
				return fmt.Errorf("%s.label %q is duplicated", optionPrefix, option.Label)
			}
			labels[option.Label] = true
			if utf8.RuneCountInString(option.Description) > MaxDescRunes {
				return fmt.Errorf("%s.description must be at most %d characters", optionPrefix, MaxDescRunes)
			}
		}
	}
	return nil
}
