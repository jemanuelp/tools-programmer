package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type Runtime struct {
	ctx                      context.Context
	root, db, binary, origin string
	env                      []string
	process                  *ownedProcess
	provider                 *Provider
	server                   *http.Server
	client                   *http.Client
	mu                       sync.Mutex
	roots                    []string
}

func startRuntime(ctx context.Context, root, binary, db string, ask bool) (*Runtime, error) {
	if err := pluginReady("/opt/plugin"); err != nil {
		return nil, err
	}
	if err := privateDirs(root); err != nil {
		return nil, err
	}
	r := &Runtime{ctx: ctx, root: root, db: db, binary: binary, env: privateEnv(root), provider: newProvider(), client: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_refused") }}}
	if _, err := command(ctx, root, r.env, "git", "init", "-q", root+"/repo"); err != nil {
		return nil, err
	}
	for _, file := range []string{"plugin.ts", "observer.mjs", "parent-snapshot.mjs", "session-snapshot.mjs", "runtime-read.mjs", "transport.mjs", "context.mjs", "advisory.mjs", "diagnostics.mjs", "issue-tracking.md"} {
		b, err := os.ReadFile(sourceRoot + "/integrations/opencode/" + file)
		if err != nil {
			return nil, errors.New("observer_source")
		}
		if os.WriteFile(root+"/observer/"+file, b, 0600) != nil {
			return nil, errors.New("observer_copy")
		}
	}
	if os.Symlink("/opt/plugin/node_modules", root+"/observer/node_modules") != nil {
		return nil, errors.New("plugin_dependencies")
	}
	// The pinned config loader also checks its global plugin package. Preseed the
	// same locked dependency directory rather than allowing an implicit npm fetch.
	configDir := root + "/config/opencode"
	if os.MkdirAll(configDir, 0700) != nil {
		return nil, errors.New("runtime_config")
	}
	for _, file := range []string{"package.json", "package-lock.json"} {
		b, err := os.ReadFile("/opt/plugin/" + file)
		if err != nil {
			return nil, errors.New("plugin_dependencies")
		}
		if os.WriteFile(configDir+"/"+file, b, 0600) != nil {
			return nil, errors.New("plugin_dependencies")
		}
	}
	if os.Symlink("/opt/plugin/node_modules", configDir+"/node_modules") != nil {
		return nil, errors.New("plugin_dependencies")
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("provider_listen")
	}
	r.server = &http.Server{Handler: r.provider, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second}
	go r.server.Serve(l)
	assign := "allow"
	if ask {
		assign = "ask"
	}
	permission := map[string]any{
		"*":                       "deny",
		"tools_programmer_assign": map[string]string{"*": "deny", "lab#1": assign, "lab#2": assign},
		"bash":                    map[string]string{"*": "deny", "sleep *": "allow"},
		"task":                    map[string]string{"*": "deny", "worker": "allow"},
		"question":                "allow",
	}
	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"model":   "lab/lab", "small_model": "lab/lab", "default_agent": "lab",
		"enabled_providers": []string{"lab"}, "share": "disabled", "autoupdate": false,
		"plugin": []any{[]any{
			"file://" + root + "/observer/plugin.ts",
			map[string]string{"binary": binary, "database": db},
		}},
		"provider": map[string]any{"lab": map[string]any{
			"npm": "@ai-sdk/openai-compatible", "name": "Controlled laboratory",
			"options": map[string]any{"baseURL": "http://" + l.Addr().String() + "/v1", "apiKey": "fixture-not-a-secret"},
			"models": map[string]any{"lab": map[string]any{
				"name": "lab", "tool_call": true,
				"limit": map[string]int{"context": 65536, "output": 4096},
			}},
		}},
		"permission": permission,
		"agent": map[string]any{
			"lab":    map[string]any{"mode": "primary", "model": "lab/lab", "permission": permission},
			"worker": map[string]any{"mode": "subagent", "model": "lab/lab", "permission": permission},
		},
	}
	b, _ := json.Marshal(config)
	if os.WriteFile(configDir+"/opencode.json", b, 0600) != nil {
		r.close()
		return nil, errors.New("runtime_config")
	}
	r.env = append(r.env, "OPENCODE_CONFIG="+configDir+"/opencode.json")
	// Reserve an ephemeral loopback port, then reuse that exact origin on restart.
	port, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		r.close()
		return nil, errors.New("runtime_port")
	}
	r.origin = "http://" + port.Addr().String()
	port.Close()
	r.provider.Verify = r.successfulCall
	if err := r.start(); err != nil {
		r.close()
		return nil, err
	}
	return r, nil
}

func pluginReady(root string) error {
	b, err := os.ReadFile(root + "/node_modules/@opencode-ai/plugin/package.json")
	var pkg struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal(b, &pkg) != nil || pkg.Version != "1.18.30" {
		return errors.New("plugin_dependencies_missing_or_wrong_version")
	}
	for _, name := range []string{"package.json", "package-lock.json", "node_modules/@opencode-ai/plugin/dist/index.js", "node_modules/@opencode-ai/sdk/dist/index.js"} {
		st, err := os.Stat(root + "/" + name)
		if err != nil || !st.Mode().IsRegular() {
			return errors.New("plugin_dependencies_incomplete")
		}
	}
	return nil
}
func (r *Runtime) start() error {
	u, _ := url.Parse(r.origin)
	p, err := startProcess(r.ctx, r.root+"/repo", r.env, "opencode", "serve", "--hostname", "127.0.0.1", "--port", u.Port())
	if err != nil {
		return err
	}
	r.process = p
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	return wait(ctx, func() (bool, error) {
		select {
		case <-p.done:
			return false, errors.New("runtime_exited")
		default:
		}
		var health struct {
			Healthy bool   `json:"healthy"`
			Version string `json:"version"`
		}
		err := r.request(ctx, "GET", "/global/health", nil, &health)
		return err == nil && health.Healthy && health.Version == "1.18.30", nil
	})
}
func (r *Runtime) close() {
	if r == nil {
		return
	}
	r.process.stop()
	if r.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r.server.Shutdown(ctx)
	}
	r.client.CloseIdleConnections()
}
func (r *Runtime) request(ctx context.Context, method, path string, input, out any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return errors.New("runtime_request")
		}
		body = bytes.NewReader(b)
	}
	sep := "?"
	if bytes.ContainsRune([]byte(path), '?') {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, method, r.origin+path+sep+"directory="+url.QueryEscape(r.root+"/repo"), body)
	if err != nil {
		return errors.New("runtime_request")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := r.client.Do(req)
	if err != nil {
		return errors.New("runtime_transport")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("runtime_status")
	}
	if out == nil {
		return nil
	}
	if json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(out) != nil {
		return errors.New("runtime_response")
	}
	return nil
}
func (r *Runtime) session() (string, error) {
	var s struct {
		ID string `json:"id"`
	}
	err := r.request(r.ctx, "POST", "/session", map[string]string{"title": "Laboratory fixture"}, &s)
	if err != nil {
		return "", err
	}
	if !nativeID(s.ID) {
		return "", errors.New("runtime_session_id")
	}
	r.mu.Lock()
	r.roots = append(r.roots, s.ID)
	r.mu.Unlock()
	return s.ID, nil
}
func nativeID(s string) bool {
	if len(s) < 5 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func (r *Runtime) prompt(session, turn string) error {
	return r.request(r.ctx, "POST", "/session/"+session+"/prompt_async", map[string]any{"model": map[string]string{"providerID": "lab", "modelID": "lab"}, "agent": "lab", "parts": []any{map[string]string{"type": "text", "text": "[lab:" + turn + "] Execute the bounded fixture."}}}, nil)
}

type runtimePart struct {
	Type   string `json:"type"`
	CallID string `json:"callID"`
	Tool   string `json:"tool"`
	State  struct {
		Status   string `json:"status"`
		Metadata struct {
			SessionID  string `json:"sessionId"`
			ParentID   string `json:"parentSessionId"`
			JobID      string `json:"jobId"`
			Background bool   `json:"background"`
		} `json:"metadata"`
		Time struct{ Start, End int64 } `json:"time"`
	} `json:"state"`
}

type runtimeMessage struct {
	Info struct {
		ID        string                             `json:"id"`
		SessionID string                             `json:"sessionID"`
		ParentID  string                             `json:"parentID"`
		Role      string                             `json:"role"`
		Finish    string                             `json:"finish"`
		Error     json.RawMessage                    `json:"error"`
		Time      struct{ Created, Completed int64 } `json:"time"`
	} `json:"info"`
	Parts []runtimePart `json:"parts"`
}

func nativeTurnComplete(messages []runtimeMessage, session string) bool {
	if len(messages) == 0 || len(messages) >= 20 {
		return false
	}
	user := ""
	var created int64
	for _, m := range messages {
		if m.Info.SessionID != session {
			return false
		}
		if m.Info.Role == "user" && m.Info.Time.Created > created {
			user = m.Info.ID
			created = m.Info.Time.Created
		}
	}
	if !nativeID(user) {
		return false
	}
	finished := false
	for _, m := range messages {
		if m.Info.Role == "user" && m.Info.Time.Created == created && m.Info.ID != user {
			return false
		}
		if m.Info.Role != "assistant" || m.Info.ParentID != user {
			continue
		}
		if m.Info.Time.Completed <= 0 || (len(m.Info.Error) > 0 && !bytes.Equal(m.Info.Error, []byte("null"))) {
			return false
		}
		for _, p := range m.Parts {
			if p.Type == "tool" && (p.State.Status == "pending" || p.State.Status == "running") {
				return false
			}
		}
		finished = finished || m.Info.Finish == "stop"
	}
	return finished
}

func (r *Runtime) quiescent(ctx context.Context, sessions ...string) (bool, error) {
	idle := func() (bool, error) {
		var states map[string]struct {
			Type string `json:"type"`
		}
		if err := r.request(ctx, "GET", "/session/status", nil, &states); err != nil {
			return false, err
		}
		if len(states) > 128 {
			return false, errors.New("runtime_status_bound")
		}
		for _, id := range sessions {
			if state, ok := states[id]; ok && state.Type != "idle" {
				return false, nil
			}
		}
		return true, nil
	}
	if ok, err := idle(); !ok || err != nil {
		return false, err
	}
	for _, id := range sessions {
		var messages []runtimeMessage
		if err := r.request(ctx, "GET", "/session/"+id+"/message?limit=20", nil, &messages); err != nil {
			return false, err
		}
		if !nativeTurnComplete(messages, id) {
			return false, nil
		}
	}
	return idle()
}

func (r *Runtime) parts(session string) ([]runtimePart, error) {
	var msgs []struct {
		Parts []runtimePart `json:"parts"`
	}
	err := r.request(r.ctx, "GET", "/session/"+session+"/message?limit=20", nil, &msgs)
	if err != nil {
		return nil, err
	}
	if len(msgs) >= 20 {
		return nil, errors.New("runtime_history_bound")
	}
	var result []runtimePart
	for _, m := range msgs {
		result = append(result, m.Parts...)
	}
	return result, nil
}
func (r *Runtime) successfulCall(call string) bool {
	r.mu.Lock()
	ids := append([]string{}, r.roots...)
	r.mu.Unlock()
	for _, root := range ids {
		var children []struct {
			ID string `json:"id"`
		}
		if r.request(r.ctx, "GET", "/session/"+root+"/children", nil, &children) == nil && len(children) <= 8 {
			for _, child := range children {
				if nativeID(child.ID) {
					ids = append(ids, child.ID)
				}
			}
		}
	}
	for _, id := range ids {
		parts, err := r.parts(id)
		if err != nil {
			continue
		}
		for _, part := range parts {
			if part.CallID == call {
				return part.State.Status == "completed"
			}
		}
	}
	return false
}
func (r *Runtime) complete(turn string) error {
	if err := wait(r.ctx, func() (bool, error) {
		if e := r.provider.failure(); e != "" {
			return false, errors.New(e)
		}
		return r.provider.completed(turn), nil
	}); err != nil {
		return err
	}
	if err := wait(r.ctx, func() (bool, error) {
		var status map[string]struct {
			Type string `json:"type"`
		}
		if err := r.request(r.ctx, "GET", "/session/status", nil, &status); err != nil {
			return false, err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, id := range r.roots {
			if s, ok := status[id]; ok && s.Type != "idle" {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		return err
	}
	return boundedPause(r.ctx, 200*time.Millisecond)
}
func wait(ctx context.Context, fn func() (bool, error)) error {
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	for {
		ok, err := fn()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("deadline")
		case <-timer.C:
		}
	}
}
func assignment(issue int) []Call {
	return []Call{{"tools_programmer_assign", map[string]any{"project": "lab", "issue": issue}}}
}
func sleepCall(seconds int) Call {
	return Call{"bash", map[string]any{"command": "sleep " + strconv.Itoa(seconds), "description": "Bounded laboratory sleep", "timeout": (seconds + 5) * 1000}}
}
func taskCall(turn string, background bool, child string) Call {
	args := map[string]any{"description": "Laboratory child", "prompt": "[lab:" + turn + "] Execute the bounded fixture.", "subagent_type": "worker"}
	if background {
		args["background"] = true
	}
	if child != "" {
		args["task_id"] = child
	}
	return Call{"task", args}
}

func loadSource() (Source, error) {
	var s Source
	b, err := os.ReadFile(filepath.Join(sourceRoot, ".lab-source.json"))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return s, errors.New("source_manifest")
	}
	return s, VerifySource(sourceRoot, s)
}
