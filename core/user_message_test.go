package core

import (
	"reflect"
	"testing"
)

func TestUserMessageDisplayContent(t *testing.T) {
	block := func(text string) ContentBlock {
		return ContentBlock{Kind: BlockText, Text: text}
	}
	context := block("<context>\ndate: now\n</context>")
	update := block("<system-update>\n# Your memory\n</system-update>")
	plain := block("Cihuyy")

	cases := []struct {
		name    string
		message UserMessage
		want    []ContentBlock
	}{
		{
			name:    "field drops one context block",
			message: UserMessage{ContextBlocks: 1, Content: []ContentBlock{context, plain}},
			want:    []ContentBlock{plain},
		},
		{
			name:    "field drops context and system update",
			message: UserMessage{ContextBlocks: 2, Content: []ContentBlock{context, update, plain}},
			want:    []ContentBlock{plain},
		},
		{
			name:    "fallback drops leading context block",
			message: UserMessage{Content: []ContentBlock{context, plain}},
			want:    []ContentBlock{plain},
		},
		{
			name:    "fallback drops context and system update",
			message: UserMessage{Content: []ContentBlock{context, update, plain}},
			want:    []ContentBlock{plain},
		},
		{
			name:    "fallback stops at the first plain block",
			message: UserMessage{Content: []ContentBlock{context, block("hello <context> is cool")}},
			want:    []ContentBlock{block("hello <context> is cool")},
		},
		{
			name:    "plain text that mentions context is untouched",
			message: UserMessage{Content: []ContentBlock{block("hello <context> is cool")}},
			want:    []ContentBlock{block("hello <context> is cool")},
		},
		{
			name:    "count beyond length does not panic",
			message: UserMessage{ContextBlocks: 5, Content: []ContentBlock{context}},
			want:    []ContentBlock{},
		},
		{
			name:    "empty content stays empty",
			message: UserMessage{},
			want:    nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.message.DisplayContent()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DisplayContent()=%v want %v", got, tc.want)
			}
		})
	}
}
