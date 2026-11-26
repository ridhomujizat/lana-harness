package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// stubAgyHook stubs the pane check, the git root and the report for the agy
// hook tests.
func stubAgyHook(t *testing.T, inPane bool) *[]string {
	t.Helper()
	oldInPane, oldRoot, oldReport := hookInPane, hookGitRoot, workbenchAgyReport
	t.Cleanup(func() { hookInPane, hookGitRoot, workbenchAgyReport = oldInPane, oldRoot, oldReport })
	hookInPane = func() (string, bool) {
		if inPane {
			return "ws-7", true
		}
		return "", false
	}
	hookGitRoot = func(dir string) string { return dir + "-root" }
	var calls []string
	workbenchAgyReport = func(id, cwd, target string) int {
		calls = append(calls, id+"|"+cwd+"|"+target)
		return 0
	}
	return &calls
}

func TestAgySessionStartInPane(t *testing.T) {
	stubAgyHook(t, true)
	var out bytes.Buffer
	in := `{"conversationId":"c1","workspacePaths":["/ws"],"cwd":"/repo/sub","hookEventName":"SessionStart"}`
	if code := workbenchAgySessionStart(strings.NewReader(in), &out); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got agyInjectAnswer
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout %q: %v", out.String(), err)
	}
	if len(got.InjectSteps) != 1 {
		t.Fatalf("injectSteps = %+v", got.InjectSteps)
	}
	text := got.InjectSteps[0].SystemMessage.SystemMessage
	if text != workbenchHookText("ws-7", "/repo/sub-root") {
		t.Fatalf("text = %q", text)
	}
	// The exact wire shape agy reads.
	if !strings.HasPrefix(out.String(), `{"injectSteps":[{"systemMessage":{"systemMessage":"You are running inside a Lana Workbench pane`) {
		t.Fatalf("stdout = %s", out.String())
	}
}

func TestAgySessionStartFallsBackToWorkspace(t *testing.T) {
	stubAgyHook(t, true)
	var out bytes.Buffer
	workbenchAgySessionStart(strings.NewReader(`{"workspacePaths":["/ws"]}`), &out)
	if !strings.Contains(out.String(), "/ws-root") {
		t.Fatalf("stdout = %s, want the workspace root", out.String())
	}
}

func TestAgySessionStartOutsidePaneAndGarbage(t *testing.T) {
	stubAgyHook(t, false)
	var out bytes.Buffer
	if code := workbenchAgySessionStart(strings.NewReader(`{"cwd":"/repo"}`), &out); code != 0 || strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("outside pane: exit %d stdout %q", code, out.String())
	}
	stubAgyHook(t, true)
	out.Reset()
	if code := workbenchAgySessionStart(strings.NewReader("not json"), &out); code != 0 || strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("garbage: exit %d stdout %q", code, out.String())
	}
}

func TestAgyPostEditPathExtraction(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"write_file Path", `{"cwd":"/repo","toolCall":{"id":"1","name":"write_file","args":{"Path":"/repo/a.go","Content":"x","Overwrite":true}}}`, "ws-7|/repo|/repo/a.go"},
		{"edit_file Path", `{"cwd":"/repo","toolCall":{"name":"edit_file","args":{"Path":"b.go","Target":"x","Replacement":"y"}}}`, "ws-7|/repo|b.go"},
		{"TargetFile", `{"cwd":"/repo","toolCall":{"name":"replace_file_content","args":{"TargetFile":"/w/c.go"}}}`, "ws-7|/repo|/w/c.go"},
		{"AbsolutePath", `{"toolCall":{"name":"create_file","args":{"AbsolutePath":"/w/d.go"}},"workspacePaths":["/ws"]}`, "ws-7|/ws|/w/d.go"},
		{"args as string", `{"cwd":"/repo","toolCall":{"name":"write_to_file","args":"{\"TargetFile\":\"/w/e.go\"}"}}`, "ws-7|/repo|/w/e.go"},
		{"no path", `{"cwd":"/repo","toolCall":{"name":"write_file","args":{"Content":"x"}}}`, "ws-7|/repo|"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := stubAgyHook(t, true)
			var out bytes.Buffer
			if code := workbenchAgyPostEdit(strings.NewReader(tc.in), &out); code != 0 {
				t.Fatalf("exit = %d", code)
			}
			if strings.TrimSpace(out.String()) != "{}" {
				t.Fatalf("stdout = %q, want {}", out.String())
			}
			if len(*calls) != 1 || (*calls)[0] != tc.want {
				t.Fatalf("report = %v, want %q", *calls, tc.want)
			}
		})
	}
}

func TestAgyPostEditGarbageOutsidePaneAndPanic(t *testing.T) {
	calls := stubAgyHook(t, true)
	var out bytes.Buffer
	if code := workbenchAgyPostEdit(strings.NewReader("{{{"), &out); code != 0 || strings.TrimSpace(out.String()) != "{}" || len(*calls) != 0 {
		t.Fatalf("garbage: exit %d stdout %q calls %v", code, out.String(), *calls)
	}

	calls = stubAgyHook(t, false)
	out.Reset()
	workbenchAgyPostEdit(strings.NewReader(`{"cwd":"/r","toolCall":{"args":{"Path":"/r/a"}}}`), &out)
	if strings.TrimSpace(out.String()) != "{}" || len(*calls) != 0 {
		t.Fatalf("outside pane: stdout %q calls %v", out.String(), *calls)
	}

	stubAgyHook(t, true)
	workbenchAgyReport = func(string, string, string) int { panic("boom") }
	out.Reset()
	if code := workbenchAgyPostEdit(strings.NewReader(`{"cwd":"/r","toolCall":{"args":{"Path":"/r/a"}}}`), &out); code != 0 || strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("panic: exit %d stdout %q", code, out.String())
	}
}

func TestWorkbenchHookAgentFlag(t *testing.T) {
	for _, args := range [][]string{
		{"post-edit"},
		{"pre-edit", "--agent", "agy"},
		{"session-start", "--agent", "nope"},
		{"session-start", "--bogus"},
	} {
		if code := cmdWorkbenchHook(args); code != 2 {
			t.Errorf("%v exit = %d, want 2", args, code)
		}
	}
}
