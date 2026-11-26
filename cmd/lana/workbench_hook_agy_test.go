package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

// stubAgyHook stubs the pane check, the git root and the report for the agy
// hook tests.
func stubAgyHook(t *testing.T, inPane bool) *[]string {
	t.Helper()
	oldInPane, oldRoot, oldReport, oldRecord := hookInPane, hookGitRoot, workbenchAgyReport, workbenchAgyRecord
	t.Cleanup(func() {
		hookInPane, hookGitRoot, workbenchAgyReport, workbenchAgyRecord = oldInPane, oldRoot, oldReport, oldRecord
	})
	workbenchAgyRecord = func(string, string) {}
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

// AC-WAG-001: inside a pane both hooks record the conversation for the core,
// under LANA_WORKBENCH_HOME, with the hook's HOME; the answers are unchanged.
func TestAgyHooksRecordConversation(t *testing.T) {
	stubAgyHook(t, true)
	workbenchAgyRecord = recordAgyConversation
	lanaHome := t.TempDir()
	account := t.TempDir()
	t.Setenv("LANA_WORKBENCH_HOME", lanaHome)
	t.Setenv("HOME", account)
	const conv = "7f3c2a10-1b2c-4d5e-8f90-a1b2c3d4e5f6"
	var out bytes.Buffer
	workbenchAgySessionStart(strings.NewReader(`{"conversationId":"`+conv+`","cwd":"/repo"}`), &out)
	if !strings.Contains(out.String(), "injectSteps") {
		t.Fatalf("stdout = %s", out.String())
	}
	data, err := os.ReadFile(filepath.Join(lanaHome, "workbench", "agents", "ws-7.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec workbench.AgentRecord
	if err := json.Unmarshal(data, &rec); err != nil || rec.Agent != "agy" || rec.ConversationID != conv || rec.Home != account {
		t.Fatalf("record = %s (%v)", data, err)
	}
	if info, err := os.Stat(filepath.Join(lanaHome, "workbench", "agents", "ws-7.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode = %v, %v", info, err)
	}

	// The post-edit hook records too, and still answers {}.
	const next = "9a8b7c6d-1b2c-4d5e-8f90-a1b2c3d4e5f6"
	out.Reset()
	workbenchAgyPostEdit(strings.NewReader(`{"conversationId":"`+next+`","toolCall":{"name":"write_file","args":{"Path":"/repo/a.go"}}}`), &out)
	if strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("post-edit stdout = %q", out.String())
	}
	data, _ = os.ReadFile(filepath.Join(lanaHome, "workbench", "agents", "ws-7.json"))
	if !strings.Contains(string(data), next) {
		t.Fatalf("record after post-edit = %s", data)
	}
}

// Outside a pane, or with an id that is not shell-safe, nothing is written.
func TestAgyHooksRecordNothing(t *testing.T) {
	lanaHome := t.TempDir()
	t.Setenv("LANA_WORKBENCH_HOME", lanaHome)
	stubAgyHook(t, false)
	workbenchAgyRecord = recordAgyConversation
	var out bytes.Buffer
	workbenchAgySessionStart(strings.NewReader(`{"conversationId":"7f3c2a10-1b2c-4d5e-8f90-a1b2c3d4e5f6"}`), &out)
	stubAgyHook(t, true)
	workbenchAgyRecord = recordAgyConversation
	for _, bad := range []string{"c1", "x; rm -rf ~", "../../etc/passwd"} {
		out.Reset()
		workbenchAgySessionStart(strings.NewReader(`{"conversationId":"`+bad+`"}`), &out)
	}
	if entries, _ := os.ReadDir(filepath.Join(lanaHome, "workbench", "agents")); len(entries) != 0 {
		t.Fatalf("records written: %v", entries)
	}
}
