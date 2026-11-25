package nodered

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestValidateFlowReportsFunctionSyntax(t *testing.T) {
	issues := ValidateFlow(RawFlow(`{"id":"tab","nodes":[{"id":"fn","type":"function","x":1,"y":1,"func":"const = ;"}]}`))
	if len(issues) != 1 || issues[0].Kind != IssueFunctionSyntax || !strings.Contains(issues[0].Message, `node "fn" field "func"`) {
		t.Fatalf("ValidateFlow issues = %#v", issues)
	}
}

func TestSetFlowsRefusesInvalidFunctionBeforeRequest(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("runtime contacted for invalid function: %s %s", r.Method, r.URL.Path)
	})
	flows := mustFlowArray(t, `[{"id":"tab","type":"tab"},{"id":"fn","type":"function","z":"tab","x":1,"y":1,"func":"const = ;"}]`)
	if err := c.SetFlows(context.Background(), flows); err == nil || !strings.Contains(err.Error(), `field "func"`) {
		t.Fatalf("SetFlows error = %v", err)
	}
}

func TestValidateFunctionBodySyntax(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"syntax error", "const = ;", false},
		{"top level await and return", "await foo(); return msg;", true},
		{"escaped newline in string", `const s = "line\nnext"; return s;`, true},
		{"escaped newline hint only at failing token", `const x = \n;`, false},
		{"escaped newline in string does not hint unrelated error", `const s = "line\nnext"; const = ;`, false},
		{"wrapper breakout", `} ); evil(); async function (msg, node, context, flow, global, env, RED) {`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFunctionSyntax("n1", "func", tc.body)
			if (err == nil) != tc.valid {
				t.Fatalf("validateFunctionSyntax() error = %v, want valid=%v", err, tc.valid)
			}
			if tc.name == "escaped newline hint only at failing token" && !strings.Contains(err.Error(), "literal \\n outside a string") {
				t.Fatalf("expected literal-newline hint, got %v", err)
			}
			if tc.name == "escaped newline in string does not hint unrelated error" && strings.Contains(err.Error(), "literal \\n outside a string") {
				t.Fatalf("unexpected literal-newline hint: %v", err)
			}
		})
	}
}
