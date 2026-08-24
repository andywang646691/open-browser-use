package main

import (
	"bytes"
	"strings"
	"testing"
)

type fakeScriptCall struct {
	method string
	params map[string]any
}

func runFakeScript(t *testing.T, script string, handler func(method string, params map[string]any) (map[string]any, error)) (scriptRunOutput, []fakeScriptCall, error) {
	t.Helper()
	runner := newScriptRunner(socketOptions{})
	var calls []fakeScriptCall
	runner.invoker = func(method string, params map[string]any) (map[string]any, error) {
		calls = append(calls, fakeScriptCall{method: method, params: params})
		return handler(method, params)
	}
	output, err := runner.run(script)
	return output, calls, err
}

func scriptResult(value any) map[string]any {
	return map[string]any{"result": value}
}

func scriptUserTabs() map[string]any {
	return scriptResult([]any{map[string]any{"id": float64(7), "title": "Example", "url": "https://example.com"}})
}

func TestScriptUserTabsClaimFlow(t *testing.T) {
	output, calls, err := runFakeScript(t, `
tabs = user_tabs()
claim_tab(tabs[0]["id"])
`, func(method string, _ map[string]any) (map[string]any, error) {
		switch method {
		case "getUserTabs":
			return scriptUserTabs(), nil
		case "claimUserTab":
			return scriptResult(map[string]any{"ok": true}), nil
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].method != "getUserTabs" {
		t.Errorf("first call method = %q, want getUserTabs", calls[0].method)
	}
	if calls[1].method != "claimUserTab" {
		t.Errorf("second call method = %q, want claimUserTab", calls[1].method)
	}
	tabID, ok := calls[1].params["tabId"]
	if !ok || tabID != 7 {
		t.Errorf("claimUserTab tabId = %v, want 7", tabID)
	}
	if len(output.Lines) != 1 {
		t.Fatalf("expected 1 output line for the bare claim_tab call, got %d", len(output.Lines))
	}
	if output.Lines[0].(map[string]any)["ok"] != true {
		t.Errorf("output line = %v, want claim result", output.Lines[0])
	}
}

func TestScriptCDPUsesCurrentTabAndReturnsCDPResult(t *testing.T) {
	output, calls, err := runFakeScript(t, `
open_tab("https://example.com")
tree = cdp("Accessibility.getFullAXTree")["nodes"]
print(len(tree))
box = cdp("DOM.getBoxModel", backendNodeId=5)["model"]["content"]
print(box)
`, func(method string, params map[string]any) (map[string]any, error) {
		switch method {
		case "createTab":
			return scriptResult(map[string]any{"id": float64(9), "url": "about:blank"}), nil
		case "attach":
			return scriptResult(map[string]any{}), nil
		case "executeCdp":
			cdpMethod, _ := params["method"].(string)
			switch cdpMethod {
			case "Accessibility.getFullAXTree":
				return scriptResult(map[string]any{"nodes": []any{map[string]any{"nodeId": 1}, map[string]any{"nodeId": 2}}}), nil
			case "DOM.getBoxModel":
				return scriptResult(map[string]any{"model": map[string]any{"content": []any{0, 0, 100, 50}}}), nil
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var axCall, boxCall fakeScriptCall
	for _, call := range calls {
		if call.method != "executeCdp" {
			continue
		}
		cdpMethod, _ := call.params["method"].(string)
		if cdpMethod == "Accessibility.getFullAXTree" {
			axCall = call
		}
		if cdpMethod == "DOM.getBoxModel" {
			boxCall = call
		}
	}
	if axCall.method == "" {
		t.Fatal("missing Accessibility.getFullAXTree call")
	}
	target := axCall.params["target"].(map[string]any)
	if target["tabId"] != 9 {
		t.Errorf("AX call target tabId = %v, want 9 (current tab from open_tab)", target["tabId"])
	}
	if boxCall.method == "" {
		t.Fatal("missing DOM.getBoxModel call")
	}
	commandParams := boxCall.params["commandParams"].(map[string]any)
	if commandParams["backendNodeId"] != 5 {
		t.Errorf("getBoxModel backendNodeId = %v, want 5", commandParams["backendNodeId"])
	}
	if len(output.Lines) != 3 {
		t.Fatalf("expected 3 output lines, got %d: %v", len(output.Lines), output.Lines)
	}
	if output.Lines[1] != 2 {
		t.Errorf("len(tree) printed as %v, want 2", output.Lines[1])
	}
	content, ok := output.Lines[2].([]any)
	if !ok || len(content) != 4 {
		t.Errorf("box content printed as %v, want coordinate list", output.Lines[2])
	}
}

func TestScriptEvaluateReturnsValue(t *testing.T) {
	output, calls, err := runFakeScript(t, `
open_tab("https://example.com")
print(evaluate("document.title"))
`, func(method string, params map[string]any) (map[string]any, error) {
		if method == "createTab" {
			return scriptResult(map[string]any{"id": float64(9), "url": "about:blank"}), nil
		}
		if method == "executeCdp" {
			// CDP Runtime.evaluate result shape: {result: {type, value}}.
			return scriptResult(map[string]any{
				"result": map[string]any{"type": "string", "value": "Example Domain"},
			}), nil
		}
		return scriptResult(map[string]any{}), nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(calls) < 2 {
		t.Fatalf("expected attach + executeCdp calls, got %d", len(calls))
	}
	evalCall := calls[len(calls)-1]
	commandParams := evalCall.params["commandParams"].(map[string]any)
	if commandParams["expression"] != "document.title" {
		t.Errorf("expression = %v", commandParams["expression"])
	}
	if commandParams["returnByValue"] != true {
		t.Errorf("returnByValue = %v, want true", commandParams["returnByValue"])
	}
	if len(output.Lines) != 2 || output.Lines[1] != "Example Domain" {
		t.Errorf("printed values = %v, want\nline 1: the created tab\nline 2: Example Domain", output.Lines)
	}
}

func TestScriptEvaluateRequiresCurrentTab(t *testing.T) {
	_, _, err := runFakeScript(t, `print(evaluate("1"))`, func(string, map[string]any) (map[string]any, error) {
		return scriptResult(map[string]any{}), nil
	})
	if err == nil || !strings.Contains(err.Error(), "no current tab") {
		t.Fatalf("expected no-current-tab error, got %v", err)
	}
}

func TestScriptFinalizeDefaultsKeepEmpty(t *testing.T) {
	_, calls, err := runFakeScript(t, `finalize_tabs()`, func(string, map[string]any) (map[string]any, error) {
		return scriptResult(map[string]any{"closed": 2}), nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(calls) != 1 || calls[0].method != "finalizeTabs" {
		t.Fatalf("calls = %v, want single finalizeTabs", calls)
	}
	keep, ok := calls[0].params["keep"].([]any)
	if !ok || len(keep) != 0 {
		t.Errorf("finalizeTabs keep = %v, want empty list", calls[0].params["keep"])
	}
}

func TestScriptFinalizeWithKeepDict(t *testing.T) {
	_, calls, err := runFakeScript(t, `
finalize_tabs(keep=[{"tabId": 12, "status": "deliverable"}])
`, func(string, map[string]any) (map[string]any, error) {
		return scriptResult(map[string]any{}), nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	keep := calls[0].params["keep"].([]any)
	entry, ok := keep[0].(map[string]any)
	if !ok || entry["tabId"] != 12 || entry["status"] != "deliverable" {
		t.Errorf("keep = %v, want [{\"tabId\":12,\"status\":\"deliverable\"}]", keep)
	}
}

func TestScriptHistoryKwargs(t *testing.T) {
	_, calls, err := runFakeScript(t, `history("example", limit=20)`, func(string, map[string]any) (map[string]any, error) {
		return scriptResult([]any{}), nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	params := calls[0].params
	if params["query"] != "example" || params["limit"] != 20 {
		t.Errorf("history params = %v, want query=example limit=20", params)
	}
}

func TestScriptNavigateAndNameSession(t *testing.T) {
	_, calls, err := runFakeScript(t, `
navigate(3, "https://docs.browser-use.com")
name_session("Docs scan - OBU")
`, func(method string, params map[string]any) (map[string]any, error) {
		if method == "executeCdp" {
			return scriptResult(map[string]any{"frameId": "abc"}), nil
		}
		return scriptResult(map[string]any{}), nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var navigateCall, nameCall fakeScriptCall
	for _, call := range calls {
		switch call.method {
		case "executeCdp":
			navigateCall = call
		case "nameSession":
			nameCall = call
		}
	}
	if navigateCall.method == "" {
		t.Fatal("missing navigate executeCdp call")
	}
	target := navigateCall.params["target"].(map[string]any)
	if target["tabId"] != 3 {
		t.Errorf("navigate target tabId = %v, want 3", target["tabId"])
	}
	if nameCall.method == "" || nameCall.params["name"] != "Docs scan - OBU" {
		t.Errorf("nameSession call = %v", nameCall)
	}
}

func TestScriptUnknownFunction(t *testing.T) {
	_, _, err := runFakeScript(t, `frobnicate(1)`, func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), `unknown function "frobnicate"`) {
		t.Fatalf("expected unknown function error, got %v", err)
	}
}

func TestScriptParseErrorsReportLineNumbers(t *testing.T) {
	for _, script := range []string{
		"tabs = ",
		"print(\"unterminated)",
		"claim_tab(",
		"cdp(\"x\", 1, 2)",
	} {
		_, _, err := runFakeScript(t, script, func(string, map[string]any) (map[string]any, error) {
			return nil, nil
		})
		if err == nil {
			t.Errorf("script %q: expected error", script)
			continue
		}
		if !strings.Contains(err.Error(), "line 1") {
			t.Errorf("script %q: error %q does not mention line 1", script, err)
		}
	}
}

func TestScriptUndefinedVariableReportsLine(t *testing.T) {
	_, _, err := runFakeScript(t, "print(missing)\nprint(\"ok\")", func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), `undefined variable "missing"`) || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("expected undefined variable error on line 1, got %v", err)
	}
}

func TestScriptCommentsBlankLinesAndBareLiteral(t *testing.T) {
	output, _, err := runFakeScript(t, `
# comment line
print("first")

print('second', 3)
`, func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(output.Lines) != 3 {
		t.Fatalf("lines = %v, want 3 entries", output.Lines)
	}
	if output.Lines[0] != "first" || output.Lines[1] != "second" || output.Lines[2] != 3 {
		t.Errorf("lines = %v", output.Lines)
	}
}

func TestScriptLiteralListsAndDicts(t *testing.T) {
	output, _, err := runFakeScript(t, `
items = ["a", "b", {"nested": true}]
print(items[2]["nested"])
print(items[1])
`, func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(output.Lines) != 2 || output.Lines[0] != true || output.Lines[1] != "b" {
		t.Errorf("lines = %v, want [true \"b\"]", output.Lines)
	}
}

func TestScriptSlice(t *testing.T) {
	output, _, err := runFakeScript(t, `
items = ["a", "b", "c", "d", "e"]
print(items[1:3])
print(items[:2])
print(items[3:])
print(items[0:1])
`, func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := [][]string{{"b", "c"}, {"a", "b"}, {"d", "e"}, {"a"}}
	if len(output.Lines) != len(want) {
		t.Fatalf("lines = %v", output.Lines)
	}
	for index, expected := range want {
		got, ok := output.Lines[index].([]any)
		if !ok || len(got) != len(expected) {
			t.Errorf("slice %d = %v, want %v", index, output.Lines[index], expected)
			continue
		}
		for itemIndex, item := range expected {
			if got[itemIndex] != item {
				t.Errorf("slice %d = %v, want %v", index, output.Lines[index], expected)
			}
		}
	}
}

func TestScriptSemicolonSeparatesStatements(t *testing.T) {
	output, _, err := runFakeScript(t, `print("a"); print("b")`, func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(output.Lines) != 2 || output.Lines[0] != "a" || output.Lines[1] != "b" {
		t.Errorf("lines = %v, want [a b]", output.Lines)
	}
}

func TestScriptTupleUnpacking(t *testing.T) {
	output, _, err := runFakeScript(t, `
box = [10, 20, 110, 70]
x, y = box[0], box[1]
print(x, y)
`, func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(output.Lines) != 2 || output.Lines[0] != 10 || output.Lines[1] != 20 {
		t.Errorf("lines = %v, want [10 20]", output.Lines)
	}
}

func TestScriptTupleUnpackingMismatch(t *testing.T) {
	_, _, err := runFakeScript(t, `x, y = 1`, func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "cannot unpack 1 values into 2 names") {
		t.Fatalf("expected unpack mismatch error, got %v", err)
	}
}

func TestScriptNegativeNumberAndFloatLiteral(t *testing.T) {
	output, _, err := runFakeScript(t, `
print(-3)
print(2.5)
`, func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(output.Lines) != 2 || output.Lines[0] != -3 {
		t.Fatalf("lines = %v", output.Lines)
	}
}

func TestScriptArithmetic(t *testing.T) {
	output, _, err := runFakeScript(t, `
box = [10, 20, 110, 70]
x, y = (box[0] + box[2]) / 2, (box[1] + box[3]) / 2
print(x, y)
print(3 * 4 - 2)
print(7 // 2)
print(7 % 3)
print(2.5 + 1)
print(-box[0] * 2 + 5)
`, func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(output.Lines) != 7 {
		t.Fatalf("lines = %v", output.Lines)
	}
	if output.Lines[0] != 60.0 || output.Lines[1] != 45.0 {
		t.Errorf("box center = %v, %v; want 60, 45", output.Lines[0], output.Lines[1])
	}
	if output.Lines[2] != 10 {
		t.Errorf("3*4-2 = %v, want 10", output.Lines[2])
	}
	if output.Lines[3] != 3 {
		t.Errorf("7//2 = %v, want 3", output.Lines[3])
	}
	if output.Lines[4] != 1 {
		t.Errorf("7%%3 = %v, want 1", output.Lines[4])
	}
	if output.Lines[5] != 3.5 {
		t.Errorf("2.5+1 = %v, want 3.5", output.Lines[5])
	}
	if output.Lines[6] != -15 {
		t.Errorf("-10*2+5 = %v, want -15", output.Lines[6])
	}
}

func TestScriptArithmeticStringsRejected(t *testing.T) {
	_, _, err := runFakeScript(t, `print("a" + "b")`, func(string, map[string]any) (map[string]any, error) {
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "cannot apply") {
		t.Fatalf("expected arithmetic type error, got %v", err)
	}
}

func TestRunPipedScriptHandlesTTYAndEmpty(t *testing.T) {
	options := socketOptions{}
	if _, handled, err := runPipedScript(strings.NewReader(""), options); err != nil || handled {
		t.Fatalf("empty input: handled=%v err=%v, want handled=false", handled, err)
	}
	if output, handled, err := runPipedScript(strings.NewReader("print(\"hi\")"), options); err != nil || !handled {
		t.Fatalf("piped input: handled=%v err=%v, want handled=true", handled, err)
	} else if len(output.Lines) != 1 || output.Lines[0] != "hi" {
		t.Fatalf("output = %v, want [hi]", output.Lines)
	}
}

func TestWriteScriptOutputJSONLines(t *testing.T) {
	var buffer bytes.Buffer
	output := scriptRunOutput{Lines: []any{"hi", map[string]any{"n": 1}}}
	if err := writeScriptOutput(&buffer, output); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := "\"hi\"\n{\"n\":1}\n"
	if buffer.String() != want {
		t.Errorf("output = %q, want %q", buffer.String(), want)
	}
}

func TestRootCommandPipedStdinRunsScriptMode(t *testing.T) {
	var out bytes.Buffer
	root := newRootCommand()
	root.SetArgs([]string{})
	root.SetIn(strings.NewReader("print(\"hello from script\")\n"))
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out.String() != "\"hello from script\"\n" {
		t.Errorf("output = %q, want JSON line for printed value", out.String())
	}
}

func TestEnvSessionIDIsFlagDefault(t *testing.T) {
	t.Setenv("OBU_SESSION_ID", "env-session-1")
	t.Setenv("OBU_BROWSER", "chrome")
	t.Setenv("OBU_PROFILE", "Default")
	cmd := newSimpleRPCCommand("ping", "ping", "Ping the browser backend")
	if value, _ := cmd.Flags().GetString("session-id"); value != "env-session-1" {
		t.Errorf("session-id default = %q, want env value", value)
	}
	if value, _ := cmd.Flags().GetString("browser"); value != "chrome" {
		t.Errorf("browser default = %q, want env value", value)
	}
	if value, _ := cmd.Flags().GetString("profile"); value != "Default" {
		t.Errorf("profile default = %q, want env value", value)
	}
}

func TestEnvAbsentFallsBackToCLISession(t *testing.T) {
	t.Setenv("OBU_SESSION_ID", "")
	t.Setenv("OBU_BROWSER", "")
	t.Setenv("OBU_PROFILE", "")
	cmd := newSimpleRPCCommand("ping", "ping", "Ping the browser backend")
	if value, _ := cmd.Flags().GetString("session-id"); value != defaultCLISessionID {
		t.Errorf("session-id default = %q, want %q", value, defaultCLISessionID)
	}
	if value, _ := cmd.Flags().GetString("browser"); value != "" {
		t.Errorf("browser default = %q, want empty", value)
	}
}

func TestMCPCommandEnvSessionID(t *testing.T) {
	t.Setenv("OBU_SESSION_ID", "env-mcp-session")
	cmd := newMCPCommand()
	if value, _ := cmd.Flags().GetString("session-id"); value != "env-mcp-session" {
		t.Errorf("mcp session-id default = %q, want env value", value)
	}
	t.Setenv("OBU_SESSION_ID", "")
	cmd = newMCPCommand()
	if value, _ := cmd.Flags().GetString("session-id"); value != defaultMCPSessionID {
		t.Errorf("mcp session-id default = %q, want %q", value, defaultMCPSessionID)
	}
}

func TestRootCommandPipedStdinRespectsSessionEnv(t *testing.T) {
	t.Setenv("OBU_SESSION_ID", "env-heredoc-session")
	root := newRootCommand()
	if value, _ := root.Flags().GetString("session-id"); value != "env-heredoc-session" {
		t.Errorf("root session-id default = %q, want env value", value)
	}
}
