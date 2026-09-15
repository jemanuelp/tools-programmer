package lab

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOwnedPromptMarkerRequiresActualSystemRequest(t *testing.T) {
	for _, role := range []string{"system", "user", "assistant"} {
		t.Run(role, func(t *testing.T) {
			p := newProvider()
			p.add("advisory")
			body := `{"messages":[{"role":"` + role + `","content":"tools-programmer:issue-tracking:v1"},{"role":"user","content":"[lab:advisory]"}],"tools":[{"function":{"name":"tools_programmer_assign","parameters":{}}}]}`
			w := httptest.NewRecorder()
			p.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
			if w.Code != 200 || p.ownedPromptSeen("advisory") != (role == "system") {
				t.Fatal(w.Code, p.failure())
			}
		})
	}
}
