package lab

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func egress(f *fixture, r *Result) error {
	interfaces, err := net.Interfaces()
	if err != nil {
		return errors.New("network_interfaces")
	}
	for _, i := range interfaces {
		if i.Flags&net.FlagLoopback == 0 {
			return errors.New("external_interface_present")
		}
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("loopback_listen")
	}
	defer l.Close()
	accepted := make(chan bool, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			c.Close()
		}
		accepted <- err == nil
	}()
	c, err := net.DialTimeout("tcp4", l.Addr().String(), time.Second)
	if err != nil {
		return errors.New("loopback_unreachable")
	}
	c.Close()
	select {
	case ok := <-accepted:
		if err := r.check("loopback_reachable", ok, 0, 0); err != nil {
			return err
		}
	case <-f.ctx.Done():
		return errors.New("deadline")
	}
	for _, address := range []string{"1.1.1.1:443", "8.8.8.8:53", "172.17.0.1:80", "[2606:4700:4700::1111]:443"} {
		c, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err == nil {
			c.Close()
			return errors.New("runtime_egress_allowed")
		}
	}
	return r.check("os_egress_blocked", true, 4, 4)
}

func missingDependencies(f *fixture, r *Result) error {
	if err := r.check("runtime_missing_plugin_preflight", pluginReady(f.root) != nil, 0, 0); err != nil {
		return err
	}
	// A plain module import must fail locally, not trigger an npm/Bun installation.
	_, err := command(f.ctx, f.root, privateEnv(f.root), "node", "--input-type=module", "-e", `await import('@opencode-ai/plugin')`)
	if err := r.check("missing_plugin_fails", err != nil, 0, 0); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(f.root, "node_modules")); !os.IsNotExist(err) {
		return errors.New("runtime_created_dependencies")
	}
	env := append(privateEnv(f.root), "GOMODCACHE="+f.root+"/empty-modules", "GOCACHE="+f.root+"/empty-build")
	_, err = command(f.ctx, sourceRoot, env, "go", "build", "-mod=readonly", "-buildvcs=false", "-o", f.root+"/missing-product", "./cmd/tools-programmer")
	return r.check("missing_go_dependencies_fail_offline", err != nil, 0, 0)
}

func sourceControl(f *fixture, r *Result) error {
	s, err := loadSource()
	if err != nil {
		return err
	}
	target := f.root + "/changed-source"
	if os.Mkdir(target, 0700) != nil {
		return errors.New("source_control_directory")
	}
	paths := []string{}
	for _, file := range s.Files {
		paths = append(paths, file.Path)
	}
	if _, err := scanSource(sourceRoot, paths, target); err != nil {
		return err
	}
	if err := VerifySource(target, s); err != nil {
		return err
	}
	file := target + "/cmd/tools-programmer/main.go"
	b, err := os.ReadFile(file)
	if err != nil {
		return errors.New("source_control_input")
	}
	if os.WriteFile(file, append(b, []byte("\nfunc init() { println(\"laboratory changed-source negative control\") }\n")...), 0644) != nil {
		return errors.New("source_control_write")
	}
	if err := r.check("changed_source_identity_rejected", VerifySource(target, s) != nil, 0, 0); err != nil {
		return err
	}
	if _, err := command(f.ctx, target, privateEnv(f.root), "go", "build", "-mod=readonly", "-buildvcs=false", "-trimpath", "-o", f.root+"/changed-product", "./cmd/tools-programmer"); err != nil {
		return err
	}
	original, err := os.ReadFile(f.binary)
	if err != nil {
		return errors.New("source_control_binary")
	}
	changed, err := os.ReadFile(f.root + "/changed-product")
	if err != nil {
		return errors.New("source_control_binary")
	}
	return r.check("dirty_bytes_change_built_binary", digest(original) != digest(changed), 0, 0)
}

func contamination(f *fixture, r *Result) error {
	if os.Getuid() == 0 {
		return errors.New("root_runtime")
	}
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENCODE_CONFIG_CONTENT", "SSH_AUTH_SOCK", "DOCKER_HOST"} {
		if os.Getenv(name) != "" {
			return errors.New("inherited_sensitive_environment")
		}
	}
	for _, path := range []string{"/var/run/docker.sock", "/root/.config", "/home/lab/.config", "/etc/opencode/opencode.json"} {
		if _, err := os.Stat(path); err == nil {
			return errors.New("unexpected_host_configuration")
		}
	}
	if err := f.start(false); err != nil {
		return err
	}
	var config struct {
		Model    string         `json:"model"`
		Plugin   []any          `json:"plugin"`
		MCP      map[string]any `json:"mcp"`
		Provider map[string]any `json:"provider"`
	}
	if err := f.runtime.request(f.ctx, "GET", "/config", nil, &config); err != nil {
		return err
	}
	if err := r.check("isolated_runtime_configuration", config.Model == "lab/lab" && len(config.Plugin) == 1 && len(config.MCP) == 0 && len(config.Provider) == 1, 0, 0); err != nil {
		return err
	}
	var sessions []any
	if err := f.runtime.request(f.ctx, "GET", "/session", nil, &sessions); err != nil {
		return err
	}
	if err := r.check("no_warmed_sessions", len(sessions) == 0, int64(len(sessions)), 0); err != nil {
		return err
	}
	id, err := f.runtime.session()
	if err != nil {
		return err
	}
	f.runtime.provider.add("unassigned", []Call{sleepCall(1)})
	if err := f.runtime.prompt(id, "unassigned"); err != nil {
		return err
	}
	if err := f.runtime.complete("unassigned"); err != nil {
		return err
	}
	s, err := f.summary(1)
	if err != nil {
		return err
	}
	return r.check("no_warmed_assignment", len(s.Segments) == 0 && s.Activity.ActiveElapsedMS == 0, s.Activity.ActiveElapsedMS, 0)
}

func timeoutCleanup(f *fixture, r *Result) error {
	ctx, cancel := context.WithTimeout(f.ctx, 150*time.Millisecond)
	defer cancel()
	p, err := startProcess(ctx, f.root, privateEnv(f.root), "sleep", "20")
	if err != nil {
		return err
	}
	pid := p.cmd.Process.Pid
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		p.stop()
		return errors.New("timeout_process_survived")
	}
	p.stop()
	if err := r.check("owned_process_group_removed", syscall.Kill(-pid, 0) != nil, 0, 0); err != nil {
		return err
	}
	// Exercise runtime cleanup at the same lifecycle seam used after scenarios.
	if err := f.start(false); err != nil {
		return err
	}
	origin := strings.TrimPrefix(f.runtime.origin, "http://")
	f.runtime.close()
	c, err := net.DialTimeout("tcp", origin, 500*time.Millisecond)
	if err == nil {
		c.Close()
		return errors.New("runtime_survived_cleanup")
	}
	return r.check("owned_runtime_port_closed", true, 0, 0)
}
