package nodered

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

const functionWrapper = "async function __nodered_function__(msg, node, context, flow, global, env, RED) {\n"

func validateFunctionSyntax(id, field, body string) error {
	source := functionWrapper + body + "\n}"
	program, err := parser.ParseFile(nil, "", source, 0, parser.WithDisableSourceMaps)
	if err != nil {
		line, column, hint := 1, 1, false
		if list, ok := err.(parser.ErrorList); ok && len(list) > 0 {
			parseErr := list[0]
			pos := parseErr.File.Position(parseErr.Offset)
			line, column = pos.Line-1, pos.Column
			if line <= 0 {
				line, column = 1, 1
			}
			hint = hasEscapedNewlineAt(body, parseErr.Offset-len(functionWrapper))
		}
		message := fmt.Sprintf("SyntaxError: node %q field %q at line %d, column %d: %s", id, field, line, column, parserMessage(err))
		if hint {
			message += `; a literal \n outside a string is not a line break; use an actual newline`
		}
		return fmt.Errorf("%s", message)
	}
	if len(program.Body) != 1 {
		return fmt.Errorf("SyntaxError: node %q field %q escapes the function body", id, field)
	}
	decl, ok := program.Body[0].(*ast.FunctionDeclaration)
	if !ok || decl.Function == nil || !decl.Function.Async || decl.Function.Body == nil || int(decl.Function.Body.RightBrace) != len(source) {
		return fmt.Errorf("SyntaxError: node %q field %q escapes the function body", id, field)
	}
	return nil
}

func hasEscapedNewlineAt(source string, target int) bool {
	var quote byte
	lineComment, blockComment := false, false
	for i := 0; i < len(source); i++ {
		c := source[i]
		if lineComment {
			if c == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if c == '*' && i+1 < len(source) && source[i+1] == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '/' && i+1 < len(source) {
			if source[i+1] == '/' {
				lineComment = true
				i++
				continue
			}
			if source[i+1] == '*' {
				blockComment = true
				i++
				continue
			}
		}
		if c == '\'' || c == '"' || c == '`' {
			quote = c
			continue
		}
		if i == target && c == '\\' && i+1 < len(source) && source[i+1] == 'n' {
			return true
		}
	}
	return false
}

func validateFunctionSyntaxInFlows(flows []json.RawMessage) error {
	var visit func(json.RawMessage) error
	visit = func(raw json.RawMessage) error {
		var node map[string]json.RawMessage
		if json.Unmarshal(raw, &node) != nil || node == nil {
			return nil
		}
		if issues := validateFunctionNode(node); len(issues) > 0 {
			return &FlowGuardError{Message: issues[0].Message}
		}
		for _, key := range []string{"nodes", "configs", "subflows"} {
			var children []json.RawMessage
			if json.Unmarshal(node[key], &children) != nil {
				continue
			}
			for _, child := range children {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, flow := range flows {
		if err := visit(flow); err != nil {
			return err
		}
	}
	return nil
}

func validateFunctionNode(node map[string]json.RawMessage) []FlowIssue {
	if stringField(node, "type") != "function" {
		return nil
	}
	var issues []FlowIssue
	for _, field := range []string{"func", "initialize", "finalize"} {
		raw, ok := node[field]
		if !ok || strings.TrimSpace(string(raw)) == "null" || strings.TrimSpace(string(raw)) == `""` {
			continue
		}
		body, ok := rawString(raw)
		if !ok {
			continue
		}
		if err := validateFunctionSyntax(nodeID(node), field, body); err != nil {
			issues = append(issues, FlowIssue{Kind: IssueFunctionSyntax, NodeID: nodeID(node), Message: err.Error()})
		}
	}
	return issues
}

func parserMessage(err error) string {
	if list, ok := err.(parser.ErrorList); ok && len(list) > 0 {
		return list[0].Message
	}
	return err.Error()
}
