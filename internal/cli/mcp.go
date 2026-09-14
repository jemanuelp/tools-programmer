package cli

import (
	"context"
	"io"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/mcpserver"
	"tools-programmer/internal/store"
)

type protocolWriter struct{ io.Writer }

func (protocolWriter) Close() error { return nil }

func selectedDatabase(path string) (string, error) {
	var err error
	if path == "" {
		path, err = store.DefaultPath()
	}
	if err != nil {
		return "", err
	}
	if path == "" || path == ":memory:" {
		return "", core.Fail("invalid_input", "database must be a filesystem path")
	}
	return filepath.Abs(path)
}

func serveMCP(ctx context.Context, path string, in io.ReadCloser, out io.Writer, options mcpserver.Options) error {
	path, err := selectedDatabase(path)
	if err != nil {
		return err
	}
	db, err := store.OpenExisting(path)
	if err != nil {
		return core.Fail("mcp_storage", "MCP requires an existing private schema v4 database; verify --db, permissions and compatibility. Initialize or migrate separately through the CLI; MCP never creates or migrates state")
	}
	defer db.Close()
	writer, ok := out.(io.WriteCloser)
	if !ok {
		writer = protocolWriter{out}
	}
	if err := mcpserver.New(db, options).Run(ctx, &mcp.IOTransport{Reader: in, Writer: writer}); err != nil && ctx.Err() == nil {
		return core.Fail("mcp_transport", "MCP transport ended with an error")
	}
	return nil
}
