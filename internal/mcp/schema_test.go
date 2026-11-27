package mcp

import (
	"context"
	"encoding/json"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jtsang4/lanesh/internal/auth"
)

func TestMCPSchemaContextBudget(t *testing.T) {
	const (
		maxTools      = 15
		maxTotalBytes = 10_000
		maxEstTokens  = 2500
		maxDescBytes  = 140
	)
	ctx := context.Background()
	st, ct := sdkmcp.NewInMemoryTransports()
	srv := NewServer(nil, auth.NewBriefings([]byte("test-secret-key-32-bytes-minimum")), Instructions(nil, nil))
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(res.Tools)
	if err != nil {
		t.Fatal(err)
	}
	totalTokens := estimateTokens(string(raw))
	t.Logf("MCP tools=%d json_bytes=%d est_tokens=%d", len(res.Tools), len(raw), totalTokens)
	for _, tool := range res.Tools {
		tb, _ := json.Marshal(tool)
		t.Logf("  %-18s desc=%3dB total=%4dB (~%3d tok)", tool.Name, len(tool.Description), len(tb), estimateTokens(string(tb)))
		if len(tool.Description) > maxDescBytes {
			t.Errorf("tool %q description is %d bytes (budget %d): keep descriptions minimal", tool.Name, len(tool.Description), maxDescBytes)
		}
	}
	if len(res.Tools) > maxTools {
		t.Errorf("registered %d MCP tools (budget %d); merge overlapping tools instead of growing the surface", len(res.Tools), maxTools)
	}
	if len(raw) > maxTotalBytes || totalTokens > maxEstTokens {
		t.Errorf("MCP tool schemas are %d bytes (~%d tokens), exceeding budget (%d bytes / %d tokens)", len(raw), totalTokens, maxTotalBytes, maxEstTokens)
	}
}
