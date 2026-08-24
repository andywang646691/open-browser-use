// Script mode: Python-style heredoc browser scripting for `obu <<'PY' ... PY`.
//
// The CLI reads the script from stdin. Helpers are pre-imported and return
// navigable JSON structures; the runtime handles all encoding, so the model
// never writes JSON-in-shell. Values are passed positionally or as named
// arguments (kwargs), mirroring CDP command params.
//
// Supported statements:
//
//	name = expr              assignment
//	expr                     expression statement (non-nil result is printed as JSON)
//	# comment                comments are ignored
//
// expr is a small subset of Python: string or number literals, true/false/
// None, lists, dicts, function calls with positional and named arguments, and
// subscript access with [index] / ["key"]. No operators are evaluated.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Runner

type scriptRunner struct {
	options      socketOptions
	sessionID    string
	turnID       string
	currentTabID int
	vars         map[string]any
	lines        []any
	invoker      func(method string, params map[string]any) (map[string]any, error)
}

type scriptRunOutput struct {
	Lines []any `json:"lines"`
}

func newScriptRunner(options socketOptions) *scriptRunner {
	sessionID := options.sessionID
	if sessionID == "" {
		sessionID = defaultCLISessionID
	}
	return &scriptRunner{
		options:   options,
		sessionID: sessionID,
		turnID:    fmt.Sprintf("%s-%d", sessionID, time.Now().UnixNano()),
		vars:      map[string]any{},
	}
}

func (runner *scriptRunner) run(script string) (scriptRunOutput, error) {
	tokens, err := lexScript(script)
	if err != nil {
		return scriptRunOutput{}, err
	}
	statements, err := parseScript(tokens)
	if err != nil {
		return scriptRunOutput{}, err
	}
	for _, statement := range statements {
		switch typed := statement.(type) {
		case scriptAssignStatement:
			value, err := runner.eval(typed.value)
			if err != nil {
				return scriptRunOutput{}, fmt.Errorf("line %d: %w", typed.line, err)
			}
			runner.vars[typed.name] = value
		case scriptTupleAssignStatement:
			values := make([]any, 0, len(typed.values))
			for _, item := range typed.values {
				value, err := runner.eval(item)
				if err != nil {
					return scriptRunOutput{}, fmt.Errorf("line %d: %w", typed.line, err)
				}
				values = append(values, value)
			}
			if len(values) != len(typed.names) {
				return scriptRunOutput{}, fmt.Errorf("line %d: cannot unpack %d values into %d names", typed.line, len(values), len(typed.names))
			}
			for index, name := range typed.names {
				runner.vars[name] = values[index]
			}
		case scriptExprStatement:
			value, err := runner.eval(typed.expr)
			if err != nil {
				return scriptRunOutput{}, fmt.Errorf("line %d: %w", typed.line, err)
			}
			if value != nil {
				runner.lines = append(runner.lines, value)
			}
		default:
			return scriptRunOutput{}, fmt.Errorf("unsupported statement %T", statement)
		}
	}
	return scriptRunOutput{Lines: runner.lines}, nil
}

func (runner *scriptRunner) invoke(method string, params map[string]any) (map[string]any, error) {
	invoker := runner.invoker
	if invoker == nil {
		invoker = runner.invokeReal
	}
	return invoker(method, params)
}

func (runner *scriptRunner) invokeReal(method string, params map[string]any) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	params["session_id"] = runner.sessionID
	params["turn_id"] = runner.turnID
	response, err := invokeWithProfile(runner.options.socketPath, runner.options.socketDir, runner.options.browser, runner.options.profile, method, params, runner.options.timeout)
	if err != nil {
		return nil, err
	}
	if errorValue, ok := response["error"].(map[string]any); ok {
		message, _ := errorValue["message"].(string)
		if message == "" {
			message = fmt.Sprintf("%v", errorValue)
		}
		return nil, errors.New(message)
	}
	return response, nil
}

func (runner *scriptRunner) invokeResult(method string, params map[string]any) (any, error) {
	response, err := runner.invoke(method, params)
	if err != nil {
		return nil, err
	}
	return response["result"], nil
}

func (runner *scriptRunner) requireCurrentTab() (int, error) {
	if runner.currentTabID > 0 {
		return runner.currentTabID, nil
	}
	return 0, errors.New("no current tab: open_tab(url) or claim_tab(id) first")
}

func (runner *scriptRunner) attach(tabID int) error {
	_, err := runner.invoke("attach", map[string]any{"tabId": tabID})
	return err
}

// ---------------------------------------------------------------------------
// Eval

type scriptArg struct {
	name  string
	value any
}

func (runner *scriptRunner) eval(node scriptExpr) (any, error) {
	switch typed := node.(type) {
	case scriptLiteralExpr:
		return typed.value, nil
	case scriptVarExpr:
		value, ok := runner.vars[typed.name]
		if !ok {
			return nil, fmt.Errorf("undefined variable %q", typed.name)
		}
		return value, nil
	case scriptListExpr:
		items := make([]any, 0, len(typed.items))
		for _, item := range typed.items {
			value, err := runner.eval(item)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		return items, nil
	case scriptDictExpr:
		pairs := make(map[string]any, len(typed.keys))
		for index, key := range typed.keys {
			value, err := runner.eval(typed.values[index])
			if err != nil {
				return nil, err
			}
			pairs[key] = value
		}
		return pairs, nil
	case scriptSubscriptExpr:
		base, err := runner.eval(typed.base)
		if err != nil {
			return nil, err
		}
		index, err := runner.eval(typed.index)
		if err != nil {
			return nil, err
		}
		return scriptSubscript(base, index)
	case scriptSliceExpr:
		base, err := runner.eval(typed.base)
		if err != nil {
			return nil, err
		}
		return scriptSlice(base, typed.start, typed.end, runner)
	case scriptBinaryExpr:
		left, err := runner.eval(typed.left)
		if err != nil {
			return nil, err
		}
		right, err := runner.eval(typed.right)
		if err != nil {
			return nil, err
		}
		return scriptArithmetic(typed.op, left, right)
	case scriptUnaryExpr:
		value, err := runner.eval(typed.operand)
		if err != nil {
			return nil, err
		}
		switch typed.op {
		case "-":
			switch number := value.(type) {
			case int:
				return -number, nil
			case float64:
				return -number, nil
			}
		}
		return nil, fmt.Errorf("cannot negate %T", value)
	case scriptCallExpr:
		args := make([]scriptArg, 0, len(typed.args))
		for _, arg := range typed.args {
			value, err := runner.eval(arg.value)
			if err != nil {
				return nil, err
			}
			args = append(args, scriptArg{name: arg.name, value: value})
		}
		if typed.name == "print" {
			if err := runner.doPrint(args); err != nil {
				return nil, err
			}
			return nil, nil
		}
		if typed.name == "len" {
			return runner.doLen(args)
		}
		return runner.callHelper(typed.name, args)
	}
	return nil, fmt.Errorf("unsupported expression %T", node)
}

func scriptSubscript(base any, index any) (any, error) {
	switch container := base.(type) {
	case []any:
		position, ok := numberAsInt(index)
		if !ok || position < 0 || position >= len(container) {
			return nil, fmt.Errorf("list index %v out of range (len %d)", index, len(container))
		}
		return container[position], nil
	case map[string]any:
		key, ok := index.(string)
		if !ok {
			return nil, fmt.Errorf("object keys must be strings, got %T", index)
		}
		value, exists := container[key]
		if !exists {
			return nil, fmt.Errorf("object has no key %q", key)
		}
		return value, nil
	}
	return nil, fmt.Errorf("cannot index %T", base)
}

func scriptSlice(base any, start scriptExpr, end scriptExpr, runner *scriptRunner) (any, error) {
	items, ok := base.([]any)
	if !ok {
		return nil, fmt.Errorf("cannot slice %T", base)
	}
	begin := 0
	if start != nil {
		value, err := runner.eval(start)
		if err != nil {
			return nil, err
		}
		position, ok := numberAsInt(value)
		if !ok {
			return nil, fmt.Errorf("slice start must be an integer, got %T", value)
		}
		begin = position
	}
	finish := len(items)
	if end != nil {
		value, err := runner.eval(end)
		if err != nil {
			return nil, err
		}
		position, ok := numberAsInt(value)
		if !ok {
			return nil, fmt.Errorf("slice end must be an integer, got %T", value)
		}
		finish = position
	}
	if begin < 0 {
		begin = 0
	}
	if finish > len(items) {
		finish = len(items)
	}
	if finish < begin {
		finish = begin
	}
	return items[begin:finish], nil
}

func scriptArithmetic(op string, left any, right any) (any, error) {
	leftInt, leftIsInt := left.(int)
	rightInt, rightIsInt := right.(int)
	leftFloat, leftIsFloat := left.(float64)
	rightFloat, rightIsFloat := right.(float64)
	if !((leftIsInt || leftIsFloat) && (rightIsInt || rightIsFloat)) {
		return nil, fmt.Errorf("cannot apply %q to %T and %T", op, left, right)
	}
	if leftIsInt && rightIsInt {
		switch op {
		case "+":
			return leftInt + rightInt, nil
		case "-":
			return leftInt - rightInt, nil
		case "*":
			return leftInt * rightInt, nil
		case "/":
			if rightInt == 0 {
				return nil, errors.New("division by zero")
			}
			return float64(leftInt) / float64(rightInt), nil
		case "//":
			if rightInt == 0 {
				return nil, errors.New("division by zero")
			}
			return leftInt / rightInt, nil
		case "%":
			if rightInt == 0 {
				return nil, errors.New("division by zero")
			}
			return leftInt % rightInt, nil
		}
	}
	leftNumber := leftFloat
	if leftIsInt {
		leftNumber = float64(leftInt)
	}
	rightNumber := rightFloat
	if rightIsInt {
		rightNumber = float64(rightInt)
	}
	switch op {
	case "+":
		return leftNumber + rightNumber, nil
	case "-":
		return leftNumber - rightNumber, nil
	case "*":
		return leftNumber * rightNumber, nil
	case "/":
		if rightNumber == 0 {
			return nil, errors.New("division by zero")
		}
		return leftNumber / rightNumber, nil
	}
	return nil, fmt.Errorf("unsupported operator %q", op)
}

func (runner *scriptRunner) doPrint(args []scriptArg) error {
	for _, arg := range args {
		if arg.name != "" {
			return errors.New("print does not take named arguments")
		}
		runner.lines = append(runner.lines, arg.value)
	}
	return nil
}

func (runner *scriptRunner) doLen(args []scriptArg) (any, error) {
	if len(args) != 1 || args[0].name != "" {
		return nil, errors.New("len takes exactly one positional argument")
	}
	value := args[0].value
	switch typed := value.(type) {
	case []any:
		return len(typed), nil
	case string:
		return len(typed), nil
	case map[string]any:
		return len(typed), nil
	}
	return nil, fmt.Errorf("len does not support %T", value)
}

// ---------------------------------------------------------------------------
// Helpers

func (runner *scriptRunner) callHelper(name string, args []scriptArg) (any, error) {
	switch name {
	case "user_tabs":
		if err := rejectScriptArgs(name, args, 0, nil); err != nil {
			return nil, err
		}
		return runner.invokeResult("getUserTabs", nil)
	case "tabs":
		if err := rejectScriptArgs(name, args, 0, nil); err != nil {
			return nil, err
		}
		return runner.invokeResult("getTabs", nil)
	case "claim_tab":
		if err := rejectScriptArgs(name, args, 1, []string{"tab_id", "tabId"}); err != nil {
			return nil, err
		}
		tabID, err := scriptIntArg(name, args, 0, "tab_id", "tabId")
		if err != nil {
			return nil, err
		}
		response, err := runner.invoke("claimUserTab", map[string]any{"tabId": tabID})
		if err != nil {
			return nil, err
		}
		runner.currentTabID = tabID
		return response["result"], nil
	case "open_tab":
		if err := rejectScriptArgs(name, args, 1, []string{"url"}); err != nil {
			return nil, err
		}
		url, err := scriptStringArg(name, args, 0, "url")
		if err != nil {
			return nil, err
		}
		return runner.helperOpenTab(url)
	case "navigate":
		if err := rejectScriptArgs(name, args, 2, []string{"tab_id", "tabId", "url"}); err != nil {
			return nil, err
		}
		tabID, err := scriptIntArg(name, args, 0, "tab_id", "tabId")
		if err != nil {
			return nil, err
		}
		url, err := scriptStringArg(name, args, 1, "url")
		if err != nil {
			return nil, err
		}
		return runner.helperNavigate(tabID, url)
	case "history":
		if err := rejectScriptArgs(name, args, 1, []string{"query", "limit"}); err != nil {
			return nil, err
		}
		query, err := scriptStringArg(name, args, 0, "query")
		if err != nil {
			return nil, err
		}
		limit := 100
		if value, ok := scriptNamedArg(args, "limit"); ok {
			number, ok := numberAsInt(value)
			if !ok {
				return nil, fmt.Errorf("history limit must be an integer, got %T", value)
			}
			limit = number
		}
		return runner.invokeResult("getUserHistory", map[string]any{"query": query, "limit": limit})
	case "name_session":
		if err := rejectScriptArgs(name, args, 1, []string{"name"}); err != nil {
			return nil, err
		}
		groupName, err := scriptStringArg(name, args, 0, "name")
		if err != nil {
			return nil, err
		}
		return runner.invokeResult("nameSession", map[string]any{"name": groupName})
	case "cdp":
		return runner.helperCDP(args)
	case "evaluate":
		if err := rejectScriptArgs(name, args, 1, []string{"expression", "js"}); err != nil {
			return nil, err
		}
		js, err := scriptStringArg(name, args, 0, "expression", "js")
		if err != nil {
			return nil, err
		}
		return runner.helperEvaluate(js)
	case "finalize_tabs":
		keep := []any{}
		for _, arg := range args {
			if arg.name != "keep" {
				return nil, fmt.Errorf("finalize_tabs got unexpected argument %q", arg.name)
			}
			value, ok := arg.value.([]any)
			if !ok {
				return nil, fmt.Errorf("finalize_tabs keep must be a list, got %T", arg.value)
			}
			keep = value
		}
		return runner.invokeResult("finalizeTabs", map[string]any{"keep": keep})
	}
	return nil, fmt.Errorf("unknown function %q (available: user_tabs, tabs, claim_tab, open_tab, navigate, history, name_session, cdp, evaluate, finalize_tabs, print)", name)
}

func (runner *scriptRunner) helperOpenTab(url string) (any, error) {
	response, err := runner.invoke("createTab", nil)
	if err != nil {
		return nil, err
	}
	tab, ok := response["result"].(map[string]any)
	if !ok {
		return nil, errors.New("createTab returned no tab object")
	}
	tabID, ok := numberAsInt(tab["id"])
	if !ok {
		return nil, errors.New("createTab result missing numeric tab id")
	}
	runner.currentTabID = tabID
	if strings.TrimSpace(url) != "" {
		if err := runner.attach(tabID); err != nil {
			return tab, err
		}
		if _, err := runner.invoke("executeCdp", map[string]any{
			"target":        map[string]any{"tabId": tabID},
			"method":        "Page.navigate",
			"commandParams": map[string]any{"url": url},
		}); err != nil {
			return tab, err
		}
	}
	return tab, nil
}

func (runner *scriptRunner) helperNavigate(tabID int, url string) (any, error) {
	if err := runner.attach(tabID); err != nil {
		return nil, err
	}
	return runner.invokeResult("executeCdp", map[string]any{
		"target":        map[string]any{"tabId": tabID},
		"method":        "Page.navigate",
		"commandParams": map[string]any{"url": url},
	})
}

func (runner *scriptRunner) helperCDP(args []scriptArg) (any, error) {
	method, err := scriptStringArg("cdp", args, 0, "method")
	if err != nil {
		return nil, err
	}
	params := map[string]any{}
	for index, arg := range args {
		if arg.name == "" && index == 0 {
			continue
		}
		if arg.name == "" {
			return nil, fmt.Errorf("cdp arguments after the method must be named, e.g. cdp(%q, backendNodeId=123)", method)
		}
		params[arg.name] = arg.value
	}
	target := map[string]any{}
	if runner.currentTabID > 0 {
		target["tabId"] = runner.currentTabID
		if err := runner.attach(runner.currentTabID); err != nil {
			return nil, err
		}
	}
	return runner.invokeResult("executeCdp", map[string]any{
		"target":        target,
		"method":        method,
		"commandParams": params,
	})
}

func (runner *scriptRunner) helperEvaluate(js string) (any, error) {
	tabID, err := runner.requireCurrentTab()
	if err != nil {
		return nil, err
	}
	if err := runner.attach(tabID); err != nil {
		return nil, err
	}
	response, err := runner.invoke("executeCdp", map[string]any{
		"target": map[string]any{"tabId": tabID},
		"method": "Runtime.evaluate",
		"commandParams": map[string]any{
			"expression":    js,
			"returnByValue": true,
		},
	})
	if err != nil {
		return nil, err
	}
	result, _ := response["result"].(map[string]any)
	if result == nil {
		return nil, errors.New("Runtime.evaluate returned no result")
	}
	if exception, ok := result["exceptionDetails"]; ok {
		return nil, fmt.Errorf("evaluate threw: %v", exception)
	}
	// CDP Runtime.evaluate returns {result: RemoteObject, exceptionDetails};
	// unwrap the RemoteObject so the value is directly usable.
	cdpResult, _ := result["result"].(map[string]any)
	if cdpResult == nil {
		return nil, errors.New("Runtime.evaluate returned no value object")
	}
	return cdpResult["value"], nil
}

// ---------------------------------------------------------------------------
// Argument helpers

func rejectScriptArgs(helper string, args []scriptArg, positionalCount int, allowedNames []string) error {
	for index, arg := range args {
		if arg.name == "" && index >= positionalCount {
			return fmt.Errorf("%s takes %d positional argument(s), got extra argument", helper, positionalCount)
		}
		if arg.name != "" && !containsString(allowedNames, arg.name) {
			return fmt.Errorf("%s got unexpected named argument %q", helper, arg.name)
		}
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func scriptPositional(args []scriptArg, index int) (any, bool) {
	if index < len(args) && args[index].name == "" {
		return args[index].value, true
	}
	return nil, false
}

func scriptNamedArg(args []scriptArg, names ...string) (any, bool) {
	for _, arg := range args {
		for _, name := range names {
			if arg.name == name {
				return arg.value, true
			}
		}
	}
	return nil, false
}

func scriptIntArg(helper string, args []scriptArg, position int, names ...string) (int, error) {
	value, ok := scriptPositional(args, position)
	if !ok {
		value, ok = scriptNamedArg(args, names...)
	}
	if !ok {
		return 0, fmt.Errorf("%s requires %s", helper, strings.Join(names, "/"))
	}
	number, ok := numberAsInt(value)
	if !ok {
		return 0, fmt.Errorf("%s expects an integer, got %T", helper, value)
	}
	return number, nil
}

func scriptStringArg(helper string, args []scriptArg, position int, names ...string) (string, error) {
	value, ok := scriptPositional(args, position)
	if !ok {
		value, ok = scriptNamedArg(args, names...)
	}
	if !ok {
		return "", fmt.Errorf("%s requires %s", helper, strings.Join(names, "/"))
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s expects a string, got %T", helper, value)
	}
	return text, nil
}

// ---------------------------------------------------------------------------
// Stdin dispatch

// runPipedScript executes a Python-style script read from reader. It reports
// handled=false when the reader is an interactive terminal or has no content,
// so callers can fall back to their normal no-argument behavior.
func runPipedScript(reader io.Reader, options socketOptions) (scriptRunOutput, bool, error) {
	if file, ok := reader.(*os.File); ok {
		stat, err := file.Stat()
		if err == nil && stat.Mode()&os.ModeCharDevice != 0 {
			return scriptRunOutput{}, false, nil
		}
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		return scriptRunOutput{}, false, err
	}
	if strings.TrimSpace(string(payload)) == "" {
		return scriptRunOutput{}, false, nil
	}
	output, err := newScriptRunner(options).run(string(payload))
	if err != nil {
		return scriptRunOutput{}, true, err
	}
	return output, true, nil
}

func writeScriptOutput(writer io.Writer, output scriptRunOutput) error {
	encoder := json.NewEncoder(writer)
	for _, line := range output.Lines {
		if err := encoder.Encode(line); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Lexer

type scriptTokenKind int

const (
	scriptTokenEOF scriptTokenKind = iota
	scriptTokenIdent
	scriptTokenString
	scriptTokenNumber
	scriptTokenPunct
)

type scriptToken struct {
	kind  scriptTokenKind
	text  string
	value any
	line  int
}

func lexScript(script string) ([]scriptToken, error) {
	var tokens []scriptToken
	line := 1
	index := 0
	for index < len(script) {
		current := script[index]
		switch {
		case current == '\n':
			line++
			index++
		case current == ' ' || current == '\t' || current == '\r':
			index++
		case current == '#':
			for index < len(script) && script[index] != '\n' {
				index++
			}
		case current == '"' || current == '\'':
			quote := current
			index++
			var builder strings.Builder
			closed := false
			for index < len(script) {
				char := script[index]
				if char == '\\' && index+1 < len(script) {
					next := script[index+1]
					switch next {
					case 'n':
						builder.WriteByte('\n')
					case 't':
						builder.WriteByte('\t')
					case 'r':
						builder.WriteByte('\r')
					default:
						builder.WriteByte(next)
					}
					index += 2
					continue
				}
				if char == quote {
					closed = true
					index++
					break
				}
				builder.WriteByte(char)
				index++
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unterminated string literal", line)
			}
			tokens = append(tokens, scriptToken{kind: scriptTokenString, value: builder.String(), line: line})
		case isScriptDigit(current):
			start := index
			index++
			for index < len(script) && isScriptDigit(script[index]) {
				index++
			}
			isFloat := false
			if index+1 < len(script) && script[index] == '.' && isScriptDigit(script[index+1]) {
				isFloat = true
				index++
				for index < len(script) && isScriptDigit(script[index]) {
					index++
				}
			}
			text := script[start:index]
			if isFloat {
				value, err := strconv.ParseFloat(text, 64)
				if err != nil {
					return nil, fmt.Errorf("line %d: invalid number %q", line, text)
				}
				tokens = append(tokens, scriptToken{kind: scriptTokenNumber, value: value, text: text, line: line})
			} else {
				value, err := strconv.ParseInt(text, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("line %d: invalid number %q", line, text)
				}
				tokens = append(tokens, scriptToken{kind: scriptTokenNumber, value: int(value), text: text, line: line})
			}
		case isScriptIdentStart(current):
			start := index
			for index < len(script) && isScriptIdentPart(script[index]) {
				index++
			}
			tokens = append(tokens, scriptToken{kind: scriptTokenIdent, text: script[start:index], line: line})
		case strings.ContainsRune("()[]{},=:;+-*/%", rune(current)):
			if current == '/' && index+1 < len(script) && script[index+1] == '/' {
				tokens = append(tokens, scriptToken{kind: scriptTokenPunct, text: "//", line: line})
				index += 2
				continue
			}
			tokens = append(tokens, scriptToken{kind: scriptTokenPunct, text: string(current), line: line})
			index++
		default:
			return nil, fmt.Errorf("line %d: unexpected character %q", line, string(current))
		}
	}
	tokens = append(tokens, scriptToken{kind: scriptTokenEOF, line: line})
	return tokens, nil
}

func isScriptDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

func isScriptIdentStart(char byte) bool {
	return char == '_' || (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
}

func isScriptIdentPart(char byte) bool {
	return isScriptIdentStart(char) || isScriptDigit(char)
}

// ---------------------------------------------------------------------------
// Parser

type scriptExpr interface{}

type scriptStatement interface{}

type scriptAssignStatement struct {
	line  int
	name  string
	value scriptExpr
}

type scriptTupleAssignStatement struct {
	line   int
	names  []string
	values []scriptExpr
}

type scriptExprStatement struct {
	line int
	expr scriptExpr
}

type scriptLiteralExpr struct {
	line  int
	value any
}

type scriptVarExpr struct {
	line int
	name string
}

type scriptListExpr struct {
	line  int
	items []scriptExpr
}

type scriptDictExpr struct {
	line   int
	keys   []string
	values []scriptExpr
}

type scriptSubscriptExpr struct {
	line  int
	base  scriptExpr
	index scriptExpr
}

type scriptSliceExpr struct {
	line  int
	base  scriptExpr
	start scriptExpr // may be nil
	end   scriptExpr // may be nil
}

type scriptBinaryExpr struct {
	line  int
	op    string
	left  scriptExpr
	right scriptExpr
}

type scriptUnaryExpr struct {
	line    int
	op      string
	operand scriptExpr
}

type scriptCallExpr struct {
	line int
	name string
	args []scriptCallArg
}

type scriptCallArg struct {
	name  string
	value scriptExpr
}

type scriptParser struct {
	tokens []scriptToken
	index  int
}

func parseScript(tokens []scriptToken) ([]scriptStatement, error) {
	parser := &scriptParser{tokens: tokens}
	statements, err := parser.parseStatements()
	if err != nil {
		return nil, err
	}
	if token := parser.peek(); token.kind != scriptTokenEOF {
		return nil, parser.errorf(token, "unexpected %q", token.text)
	}
	return statements, nil
}

func (parser *scriptParser) peek() scriptToken {
	return parser.tokens[parser.index]
}

func (parser *scriptParser) peekAt(offset int) scriptToken {
	if parser.index+offset < len(parser.tokens) {
		return parser.tokens[parser.index+offset]
	}
	return parser.tokens[len(parser.tokens)-1]
}

func (parser *scriptParser) advance() scriptToken {
	token := parser.tokens[parser.index]
	if token.kind != scriptTokenEOF {
		parser.index++
	}
	return token
}

func (parser *scriptParser) errorf(token scriptToken, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", token.line, fmt.Sprintf(format, args...))
}

func (parser *scriptParser) parseStatements() ([]scriptStatement, error) {
	var statements []scriptStatement
	for parser.peek().kind != scriptTokenEOF {
		if parser.peek().kind == scriptTokenPunct && parser.peek().text == ";" {
			parser.advance()
			continue
		}
		statement, err := parser.parseStatement()
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	return statements, nil
}

func (parser *scriptParser) parseStatement() (scriptStatement, error) {
	token := parser.peek()
	if token.kind == scriptTokenIdent {
		// name = expr, or Python-style tuple unpacking: name, name = a, b
		names := []string{token.text}
		offset := 1
		for parser.peekAt(offset).kind == scriptTokenPunct && parser.peekAt(offset).text == "," &&
			parser.peekAt(offset+1).kind == scriptTokenIdent {
			names = append(names, parser.peekAt(offset+1).text)
			offset += 2
		}
		if parser.peekAt(offset).kind == scriptTokenPunct && parser.peekAt(offset).text == "=" {
			// Advance past the names (and the commas between them when unpacking
			// a tuple: x, y = ... is tokens x , y = ...), then past '='.
			steps := 1
			if len(names) > 1 {
				steps = 2*len(names) - 1
			}
			for range steps {
				parser.advance()
			}
			parser.advance()
			if len(names) == 1 {
				values, err := parser.parseExpr()
				if err != nil {
					return nil, err
				}
				return scriptAssignStatement{line: token.line, name: names[0], value: values}, nil
			}
			values, err := parser.parseExprList()
			if err != nil {
				return nil, err
			}
			return scriptTupleAssignStatement{line: token.line, names: names, values: values}, nil
		}
	}
	expr, err := parser.parseExpr()
	if err != nil {
		return nil, err
	}
	return scriptExprStatement{line: token.line, expr: expr}, nil
}

// parseExprList parses a comma-separated expression list (the right-hand side
// of a tuple unpacking assignment).
func (parser *scriptParser) parseExprList() ([]scriptExpr, error) {
	var values []scriptExpr
	for {
		value, err := parser.parseExpr()
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		if parser.peek().kind == scriptTokenPunct && parser.peek().text == "," {
			parser.advance()
			continue
		}
		return values, nil
	}
}

func (parser *scriptParser) parseExpr() (scriptExpr, error) {
	return parser.parseAdditive()
}

func (parser *scriptParser) parseAdditive() (scriptExpr, error) {
	left, err := parser.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for {
		token := parser.peek()
		if token.kind != scriptTokenPunct || (token.text != "+" && token.text != "-") {
			return left, nil
		}
		parser.advance()
		right, err := parser.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		left = scriptBinaryExpr{line: token.line, op: token.text, left: left, right: right}
	}
}

func (parser *scriptParser) parseMultiplicative() (scriptExpr, error) {
	left, err := parser.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		token := parser.peek()
		if token.kind != scriptTokenPunct || (token.text != "*" && token.text != "/" && token.text != "//" && token.text != "%") {
			return left, nil
		}
		parser.advance()
		right, err := parser.parseUnary()
		if err != nil {
			return nil, err
		}
		left = scriptBinaryExpr{line: token.line, op: token.text, left: left, right: right}
	}
}

func (parser *scriptParser) parseUnary() (scriptExpr, error) {
	token := parser.peek()
	if token.kind == scriptTokenPunct && token.text == "-" {
		parser.advance()
		operand, err := parser.parseUnary()
		if err != nil {
			return nil, err
		}
		return scriptUnaryExpr{line: token.line, op: "-", operand: operand}, nil
	}
	return parser.parsePostfix()
}

// parsePostfix parses a primary followed by any number of subscripts and
// slices: a[0], a["key"], a[0:5]. Subscripts bind tighter than operators, so
// box[0] + box[2] is (box[0]) + (box[2]).
func (parser *scriptParser) parsePostfix() (scriptExpr, error) {
	expr, err := parser.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		token := parser.peek()
		if token.kind != scriptTokenPunct || token.text != "[" {
			return expr, nil
		}
		parser.advance()
		if parser.peek().kind == scriptTokenPunct && parser.peek().text == ":" {
			// [ : end ]
			parser.advance()
			end, err := parser.parseSliceBound()
			if err != nil {
				return nil, err
			}
			if _, err := parser.expectPunct("]"); err != nil {
				return nil, err
			}
			expr = scriptSliceExpr{line: token.line, base: expr, end: end}
			continue
		}
		first, err := parser.parseExpr()
		if err != nil {
			return nil, err
		}
		if parser.peek().kind == scriptTokenPunct && parser.peek().text == ":" {
			// [ start : end? ]
			parser.advance()
			end, err := parser.parseSliceBound()
			if err != nil {
				return nil, err
			}
			if _, err := parser.expectPunct("]"); err != nil {
				return nil, err
			}
			expr = scriptSliceExpr{line: token.line, base: expr, start: first, end: end}
			continue
		}
		if _, err := parser.expectPunct("]"); err != nil {
			return nil, err
		}
		expr = scriptSubscriptExpr{line: token.line, base: expr, index: first}
	}
}

// parseSliceBound parses the optional bound after ':' inside a slice. Returns
// nil when the next token closes the slice.
func (parser *scriptParser) parseSliceBound() (scriptExpr, error) {
	if parser.peek().kind == scriptTokenPunct && (parser.peek().text == "]" || parser.peek().text == ":") {
		return nil, nil
	}
	return parser.parseExpr()
}

func (parser *scriptParser) expectPunct(text string) (scriptToken, error) {
	token := parser.peek()
	if token.kind != scriptTokenPunct || token.text != text {
		return token, parser.errorf(token, "expected %q", text)
	}
	parser.advance()
	return token, nil
}

func (parser *scriptParser) parsePrimary() (scriptExpr, error) {
	token := parser.peek()
	switch token.kind {
	case scriptTokenString, scriptTokenNumber:
		parser.advance()
		return scriptLiteralExpr{line: token.line, value: token.value}, nil
	case scriptTokenIdent:
		switch strings.ToLower(token.text) {
		case "true":
			parser.advance()
			return scriptLiteralExpr{line: token.line, value: true}, nil
		case "false":
			parser.advance()
			return scriptLiteralExpr{line: token.line, value: false}, nil
		case "none", "null", "nil":
			parser.advance()
			return scriptLiteralExpr{line: token.line, value: nil}, nil
		}
		parser.advance()
		if parser.peek().kind == scriptTokenPunct && parser.peek().text == "(" {
			parser.advance()
			args, err := parser.parseCallArgs()
			if err != nil {
				return nil, err
			}
			if _, err := parser.expectPunct(")"); err != nil {
				return nil, err
			}
			return scriptCallExpr{line: token.line, name: token.text, args: args}, nil
		}
		return scriptVarExpr{line: token.line, name: token.text}, nil
	case scriptTokenPunct:
		switch token.text {
		case "(":
			parser.advance()
			expr, err := parser.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := parser.expectPunct(")"); err != nil {
				return nil, err
			}
			return expr, nil
		case "[":
			parser.advance()
			if parser.peek().kind == scriptTokenPunct && parser.peek().text == "]" {
				parser.advance()
				return scriptListExpr{line: token.line}, nil
			}
			items, err := parser.parseCommaSeparated("]")
			if err != nil {
				return nil, err
			}
			return scriptListExpr{line: token.line, items: items}, nil
		case "{":
			parser.advance()
			keys := []string{}
			values := []scriptExpr{}
			if parser.peek().kind == scriptTokenPunct && parser.peek().text == "}" {
				parser.advance()
				return scriptDictExpr{line: token.line, keys: keys, values: values}, nil
			}
			for {
				key := parser.peek()
				if key.kind != scriptTokenIdent && key.kind != scriptTokenString {
					return nil, parser.errorf(key, "dict keys must be identifiers or strings")
				}
				parser.advance()
				if _, err := parser.expectPunct(":"); err != nil {
					return nil, err
				}
				value, err := parser.parseExpr()
				if err != nil {
					return nil, err
				}
				keyText := key.text
				if key.kind == scriptTokenString {
					keyText, _ = key.value.(string)
				}
				keys = append(keys, keyText)
				values = append(values, value)
				next := parser.peek()
				if next.kind == scriptTokenPunct && next.text == "," {
					parser.advance()
					if parser.peek().kind == scriptTokenPunct && parser.peek().text == "}" {
						parser.advance()
						break
					}
					continue
				}
				if next.kind == scriptTokenPunct && next.text == "}" {
					parser.advance()
					break
				}
				return nil, parser.errorf(next, "expected ',' or '}' in dict literal")
			}
			return scriptDictExpr{line: token.line, keys: keys, values: values}, nil
		}
	}
	return nil, parser.errorf(token, "expected an expression")
}

func (parser *scriptParser) parseCommaSeparated(closing string) ([]scriptExpr, error) {
	var items []scriptExpr
	for {
		item, err := parser.parseExpr()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		next := parser.peek()
		if next.kind == scriptTokenPunct && next.text == "," {
			parser.advance()
			if parser.peek().kind == scriptTokenPunct && parser.peek().text == closing {
				parser.advance()
				return items, nil
			}
			continue
		}
		if next.kind == scriptTokenPunct && next.text == closing {
			parser.advance()
			return items, nil
		}
		return nil, parser.errorf(next, "expected ',' or %q", closing)
	}
}

func (parser *scriptParser) parseCallArgs() ([]scriptCallArg, error) {
	if parser.peek().kind == scriptTokenPunct && parser.peek().text == ")" {
		return nil, nil
	}
	var args []scriptCallArg
	for {
		token := parser.peek()
		if token.kind == scriptTokenIdent && parser.peekAt(1).kind == scriptTokenPunct && parser.peekAt(1).text == "=" {
			parser.advance()
			parser.advance()
			value, err := parser.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, scriptCallArg{name: token.text, value: value})
		} else {
			value, err := parser.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, scriptCallArg{value: value})
		}
		next := parser.peek()
		if next.kind == scriptTokenPunct && next.text == "," {
			parser.advance()
			if parser.peek().kind == scriptTokenPunct && parser.peek().text == ")" {
				return args, nil
			}
			continue
		}
		if next.kind == scriptTokenPunct && next.text == ")" {
			return args, nil
		}
		return nil, parser.errorf(next, "expected ',' or ')' in arguments")
	}
}
