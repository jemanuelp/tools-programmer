package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Call struct {
	Name string
	Args map[string]any
}
type turn struct {
	Stages   [][]Call
	Stage    int
	Pending  []string
	Done     bool
	Requests int
	Retry    bool
	Retried  bool
	RetryAt  time.Time
	RetryMS  int64
	First    time.Time
	Last     time.Time
}
type Provider struct {
	mu        sync.Mutex
	turns     map[string]*turn
	sequence  int
	auxiliary int
	err       string
	// Verify uses actual runtime terminal state, not a model's assertion of success.
	Verify func(string) bool
}

func newProvider() *Provider { return &Provider{turns: map[string]*turn{}} }
func (p *Provider) add(id string, stages ...[]Call) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turns[id] = &turn{Stages: stages}
}
func (p *Provider) failure() string { p.mu.Lock(); defer p.mu.Unlock(); return p.err }
func (p *Provider) completed(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.turns[id] != nil && p.turns[id].Done
}
func (p *Provider) timing(id string) (int64, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.turns[id]
	return t.Last.Sub(t.First).Milliseconds(), t.RetryMS
}

type chatRequest struct {
	Messages []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"tool_call_id"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
	Stream bool `json:"stream"`
}

var marker = regexp.MustCompile(`\[lab:([a-z0-9-]+)\]`)

func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	var q chatRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	if d.Decode(&q) != nil {
		http.Error(w, "invalid_request", 400)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fail := func(code string) {
		if p.err == "" {
			p.err = code
		}
		http.Error(w, "fixture_provider_failure", 400)
	}
	if p.err != "" {
		fail(p.err)
		return
	}
	// Tool-less title/summary calls and synthetic background notifications never
	// advance functional stages. Their count is bounded independently.
	id := ""
	for i := len(q.Messages) - 1; i >= 0; i-- {
		if q.Messages[i].Role == "user" {
			m := marker.FindSubmatch(q.Messages[i].Content)
			if len(m) > 1 {
				id = string(m[1])
			}
			break
		}
	}
	if len(q.Tools) == 0 || id == "" {
		p.auxiliary++
		if p.auxiliary > 24 {
			fail("provider_auxiliary_bound")
			return
		}
		chatReply(w, q.Stream, nil)
		return
	}
	t := p.turns[id]
	if t == nil {
		fail("provider_unknown_turn")
		return
	}
	t.Requests++
	if t.Requests > 16 {
		fail("provider_request_bound")
		return
	}
	if t.Done {
		fail("provider_duplicate_completed_turn")
		return
	}
	if t.First.IsZero() {
		t.First = time.Now()
	}
	if len(t.Pending) > 0 {
		seen := map[string]bool{}
		for _, m := range q.Messages {
			if m.Role == "tool" {
				for _, call := range t.Pending {
					if m.ToolCallID == call {
						seen[call] = true
					}
				}
			}
		}
		for _, call := range t.Pending {
			if !seen[call] {
				fail("provider_missing_result")
				return
			}
			if p.Verify == nil || !p.Verify(call) {
				fail("provider_failed_tool")
				return
			}
		}
		t.Pending = nil
		t.Stage++
	}
	if t.Retry && !t.Retried {
		t.Retried = true
		t.RetryAt = time.Now()
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"fixture retry","type":"rate_limit_error"}}`)
		return
	}
	if !t.RetryAt.IsZero() && t.RetryMS == 0 {
		t.RetryMS = time.Since(t.RetryAt).Milliseconds()
	}
	if t.Stage == len(t.Stages) {
		t.Done = true
		t.Last = time.Now()
		chatReply(w, q.Stream, nil)
		return
	}
	catalog := map[string]json.RawMessage{}
	for _, tool := range q.Tools {
		catalog[tool.Function.Name] = tool.Function.Parameters
	}
	var calls []map[string]any
	for _, c := range t.Stages[t.Stage] {
		if len(catalog[c.Name]) == 0 {
			fail("provider_tool_not_visible")
			return
		}
		if c.Name == "task" && c.Args["background"] == true && !strings.Contains(string(catalog[c.Name]), `"background"`) {
			fail("provider_background_not_visible")
			return
		}
		p.sequence++
		call := fmt.Sprintf("lab-call-%d", p.sequence)
		t.Pending = append(t.Pending, call)
		args, err := json.Marshal(c.Args)
		if err != nil {
			fail("provider_arguments")
			return
		}
		calls = append(calls, map[string]any{"index": len(calls), "id": call, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(args)}})
	}
	chatReply(w, q.Stream, calls)
}

func chatReply(w http.ResponseWriter, stream bool, calls []map[string]any) {
	reason := "stop"
	message := map[string]any{"role": "assistant", "content": "Fixture complete."}
	if len(calls) > 0 {
		reason = "tool_calls"
		message = map[string]any{"role": "assistant", "tool_calls": calls}
	}
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "lab-response", "object": "chat.completion", "created": time.Now().Unix(), "model": "lab", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": reason}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	write := func(delta any, finish any) {
		b, _ := json.Marshal(map[string]any{"id": "lab-response", "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "lab", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	write(message, nil)
	write(map[string]any{}, reason)
	io.WriteString(w, "data: [DONE]\n\n")
}

func (p *Provider) assertComplete(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != "" {
		return errors.New(p.err)
	}
	if p.turns[id] == nil || !p.turns[id].Done {
		return errors.New("provider_incomplete")
	}
	return nil
}
