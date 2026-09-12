package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// EventServerConnected in the pinned @opencode-ai/sdk 1.18.30.
const connectedEvent = "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n"

func TestReconnectPrerequisites(t *testing.T) {
	for _, tc := range []struct {
		name, mime, body string
		status           int
		stall, valid     bool
	}{
		{"connected", "text/event-stream; charset=utf-8", connectedEvent, 200, false, true},
		{"comments-crlf", "text/event-stream", ": ping\r\n\r\n" + strings.ReplaceAll(connectedEvent, "\n", "\r\n"), 200, false, true},
		{"http-error", "text/event-stream", connectedEvent, 404, false, false},
		{"wrong-mime", "application/json", connectedEvent, 200, false, false},
		{"malformed", "text/event-stream", "data: not-json\n\n", 200, false, false},
		{"wrong-shape", "text/event-stream", "data: {}\n\n", 200, false, false},
		{"missing-properties", "text/event-stream", "data: {\"type\":\"server.connected\"}\n\n", 200, false, false},
		{"no-event", "text/event-stream", ": heartbeat\n\n", 200, false, false},
		{"incomplete", "text/event-stream", strings.TrimSuffix(connectedEvent, "\n"), 200, false, false},
		{"oversized", "text/event-stream", "data: " + strings.Repeat("x", 64<<10), 200, false, false},
		{"too-many-frames", "text/event-stream", strings.Repeat(": ping\n\n", 33) + connectedEvent, 200, false, false},
		{"cancelled-read", "text/event-stream", "data: {", 200, true, false},
		{"deadline-read", "text/event-stream", "data: {", 200, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, failAt := range []int32{1, 2} {
				t.Run(fmt.Sprint(failAt), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					var requests, snapshots atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						n := requests.Add(1)
						if req.URL.Path != "/event" || req.URL.Query().Get("directory") != "/fixture/repo" || req.Header.Get("Accept") != "text/event-stream" {
							t.Error("event request must be directory scoped and negotiate SSE")
						}
						mime, body, status := "text/event-stream", connectedEvent, 200
						if n == failAt {
							mime, body, status = tc.mime, tc.body, tc.status
						}
						w.Header().Set("Content-Type", mime)
						w.WriteHeader(status)
						fmt.Fprint(w, body)
						w.(http.Flusher).Flush()
						if tc.valid || n != failAt {
							<-req.Context().Done()
							return
						}
						if tc.stall && n == failAt {
							if tc.name != "deadline-read" {
								cancel()
							}
							<-req.Context().Done()
						}
					}))
					defer server.Close()
					r := &Runtime{ctx: ctx, root: "/fixture", origin: server.URL, client: server.Client()}
					r.client.Timeout = time.Second
					err := r.reconnect(ctx, func() error { snapshots.Add(1); return nil })
					if (err == nil) != tc.valid {
						t.Fatalf("error = %v; valid = %v", err, tc.valid)
					}
					wantRequests, wantSnapshots := failAt, failAt-1
					if tc.valid {
						wantRequests, wantSnapshots = 2, 1
					}
					if requests.Load() != wantRequests || snapshots.Load() != wantSnapshots {
						t.Fatalf("follow-up counts: requests=%d snapshots=%d", requests.Load(), snapshots.Load())
					}
				})
			}
		})
	}
}

func TestBackgroundReusePrerequisites(t *testing.T) {
	for _, tc := range []struct {
		name          string
		parent, child bool
		cancel        bool
	}{
		{"both-settle", true, true, false},
		{"parent-unfinished", false, true, false},
		{"child-unfinished", true, false, false},
		{"cancelled", true, true, true},
		{"cancelled-during-wait", true, true, false},
		{"provider-failure", true, true, false},
		{"child-busy", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			var parentReads, childReads, dispatched atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/session/status":
					if tc.name == "child-busy" {
						fmt.Fprint(w, `{"child":{"type":"busy"}}`)
						return
					}
					fmt.Fprint(w, `{}`)
				case "/session/parent/message", "/session/child/message":
					id, settled, count := "parent", tc.parent, &parentReads
					if strings.Contains(req.URL.Path, "/child/") {
						id, settled, count = "child", tc.child, &childReads
					}
					// Provider completion precedes native full-turn completion.
					n := count.Add(1)
					if tc.name == "cancelled-during-wait" {
						cancel()
					}
					completed := 0
					if settled && n >= 2 {
						completed = 2
					}
					fmt.Fprintf(w, `[{"info":{"id":"user_1","sessionID":%q,"role":"user","time":{"created":1}}},{"info":{"sessionID":%q,"parentID":"user_1","role":"assistant","finish":"stop","time":{"completed":%d}}}]`, id, id, completed)
				case "/session/parent/prompt_async":
					dispatched.Add(1)
					if parentReads.Load() < 2 || childReads.Load() < 2 {
						t.Error("reuse dispatched before both full turns settled")
					}
					var body map[string]any
					if json.NewDecoder(req.Body).Decode(&body) != nil || !strings.Contains(fmt.Sprint(body), "reuse-parent") {
						t.Error("wrong reuse prompt")
					}
				default:
					t.Errorf("unexpected request: %s", req.URL.Path)
				}
			}))
			defer server.Close()
			r := &Runtime{ctx: ctx, root: "/fixture", origin: server.URL, client: server.Client(), provider: newProvider()}
			r.provider.turns["background-child"] = &turn{Done: true}
			if !r.provider.completed("background-child") {
				t.Fatal("original provider turn must already be done")
			}
			if tc.name == "provider-failure" {
				r.provider.err = "provider_failed_tool"
			}
			if tc.cancel {
				cancel()
			}
			err := r.reuseBackground(ctx, "parent", "child")
			valid := tc.name == "both-settle"
			if (err == nil) != valid || (dispatched.Load() == 1) != valid {
				t.Fatalf("error=%v dispatches=%d; valid=%v", err, dispatched.Load(), valid)
			}
			if !valid && (r.provider.turns["reuse-parent"] != nil || r.provider.turns["background-reuse"] != nil) {
				t.Fatal("failed prerequisite registered follow-up provider work")
			}
		})
	}
}
