package lab

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func providerRequest(p *Provider, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(b))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	return w
}
func requestBody(turn string, results ...string) map[string]any {
	messages := []any{map[string]any{"role": "user", "content": "[lab:" + turn + "]"}}
	for _, id := range results {
		messages = append(messages, map[string]string{"role": "tool", "tool_call_id": id, "content": "fixture result"})
	}
	return map[string]any{"messages": messages, "tools": []any{map[string]any{"function": map[string]any{"name": "tools_programmer_assign", "parameters": map[string]any{"type": "object"}}}, map[string]any{"function": map[string]any{"name": "bash", "parameters": map[string]any{"type": "object"}}}}}
}
func TestProviderRequiresActualSuccessBeforeAdvancing(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "success"}[success], func(t *testing.T) {
			p := newProvider()
			p.Verify = func(id string) bool { return success && id == "lab-call-1" }
			p.add("root", assignment(1), []Call{sleepCall(3)})
			if w := providerRequest(p, requestBody("root")); w.Code != 200 || !strings.Contains(w.Body.String(), "tools_programmer_assign") {
				t.Fatal(w.Code, w.Body.String())
			}
			w := providerRequest(p, requestBody("root", "lab-call-1"))
			if success {
				if w.Code != 200 || !strings.Contains(w.Body.String(), "bash") {
					t.Fatal(w.Code, w.Body.String())
				}
			} else {
				if w.Code != 400 || p.turns["root"].Stage != 0 {
					t.Fatal("failed assignment advanced")
				}
			}
		})
	}
}
func TestProviderRejectsMissingResultAndCompletedDuplicates(t *testing.T) {
	p := newProvider()
	p.Verify = func(string) bool { return true }
	p.add("root", assignment(1))
	providerRequest(p, requestBody("root"))
	if providerRequest(p, requestBody("root")).Code != 400 {
		t.Fatal("duplicate request reissued tools")
	}
	p = newProvider()
	p.add("root")
	providerRequest(p, requestBody("root"))
	if providerRequest(p, requestBody("root")).Code != 400 {
		t.Fatal("completed turn reused")
	}
}
func TestProviderSeparatesAuxiliaryAndFunctionalTurns(t *testing.T) {
	p := newProvider()
	p.add("root", assignment(1))
	aux := requestBody("root")
	delete(aux, "tools")
	if providerRequest(p, aux).Code != 200 || p.turns["root"].Requests != 0 {
		t.Fatal("title advanced turn")
	}
	if providerRequest(p, requestBody("unknown")).Code != 400 {
		t.Fatal("unknown turn accepted")
	}
}
func TestProviderRequiresModelVisibleTools(t *testing.T) {
	p := newProvider()
	p.add("root", []Call{taskCall("child", true, "")})
	if providerRequest(p, requestBody("root")).Code != 400 || p.failure() != "provider_tool_not_visible" {
		t.Fatal(p.failure())
	}
}
func TestProviderRetriesOnlyOnceAndStreamsCalls(t *testing.T) {
	p := newProvider()
	p.add("root", assignment(1))
	p.turns["root"].Retry = true
	b := requestBody("root")
	b["stream"] = true
	if w := providerRequest(p, b); w.Code != 429 || w.Header().Get("Retry-After") != "2" {
		t.Fatal(w.Code)
	}
	w := providerRequest(p, b)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "data: [DONE]") || !strings.Contains(w.Body.String(), "tool_calls") {
		t.Fatal(w.Code, w.Body.String())
	}
}
