package knowledge

import "testing"

// A raw newline inside a string value is invalid JSON; it is escaped and the
// reply decodes.
func TestDecodeLLMJSONRawNewline(t *testing.T) {
	var ops []compiledOp
	err := decodeLLMJSON(`[{"op":"create","title":"A","body":"line one
line two"}]`, &ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Body != "line one\nline two" {
		t.Fatalf("ops = %+v", ops)
	}
}

// A raw tab inside a string value is escaped to \t.
func TestDecodeLLMJSONRawTab(t *testing.T) {
	var ops []compiledOp
	if err := decodeLLMJSON("[{\"op\":\"create\",\"body\":\"a\tb\"}]", &ops); err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Body != "a\tb" {
		t.Fatalf("ops = %+v", ops)
	}
}

// An escaped quote must not end the string, so a raw control character after it
// is still repaired.
func TestDecodeLLMJSONEscapedQuote(t *testing.T) {
	var ops []compiledOp
	if err := decodeLLMJSON("[{\"op\":\"create\",\"body\":\"quote \\\" then\nnext\"}]", &ops); err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Body != "quote \" then\nnext" {
		t.Fatalf("ops = %+v", ops)
	}
}

// A ```json fence around the reply is stripped before decoding.
func TestDecodeLLMJSONFence(t *testing.T) {
	var reply journalReply
	if err := decodeLLMJSON("```json\n{\"notes\": [\"a\"]}\n```", &reply); err != nil {
		t.Fatal(err)
	}
	if len(reply.Notes) != 1 || reply.Notes[0] != "a" {
		t.Fatalf("reply = %+v", reply)
	}
}

// Prose before and after the JSON is ignored.
func TestDecodeLLMJSONProseAround(t *testing.T) {
	var ops []compiledOp
	text := "Here is the result:\n[{\"op\":\"create\",\"title\":\"A\"}]\nThat is all."
	if err := decodeLLMJSON(text, &ops); err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Title != "A" {
		t.Fatalf("ops = %+v", ops)
	}
}

// Valid JSON decodes unchanged.
func TestDecodeLLMJSONValid(t *testing.T) {
	var ops []compiledOp
	if err := decodeLLMJSON(`[{"op":"create","title":"A","body":"ok"}]`, &ops); err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Title != "A" || ops[0].Body != "ok" {
		t.Fatalf("ops = %+v", ops)
	}
}

// Input that is not JSON at all returns an error.
func TestDecodeLLMJSONHopeless(t *testing.T) {
	var ops []compiledOp
	if err := decodeLLMJSON("this is not json at all", &ops); err == nil {
		t.Fatal("decodeLLMJSON accepted hopeless input")
	}
}
