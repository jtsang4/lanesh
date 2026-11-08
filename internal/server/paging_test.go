package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/jtsang4/lanesh/internal/store"
)

func TestAdminListsPage(t *testing.T) {
	h := newHarness(t)
	h.startMachine("beta", true, "deploy")
	base := "/api/v1/lanes/" + h.laneID

	// Keys: newest active first, page by page; revoked ones last.
	var created struct {
		Key store.Key `json:"key"`
	}
	for i := range 5 {
		h.api(http.MethodPost, base+"/keys", map[string]any{"kind": "access", "name": fmt.Sprintf("agent-%d", i)}, &created, http.StatusCreated)
	}
	h.api(http.MethodDelete, base+"/keys/"+created.Key.ID, nil, nil, http.StatusNoContent)
	var keys []store.Key
	hdr := h.apiHeaders(http.MethodGet, base+"/keys?kind=access&limit=2&offset=4", nil, &keys, http.StatusOK)
	if hdr.Get(HeaderTotalCount) != "6" || len(keys) != 2 || keys[1].Name != "agent-4" || keys[1].RevokedAt == nil {
		t.Fatalf("keys page: %+v %v", keys, hdr)
	}
	hdr = h.apiHeaders(http.MethodGet, base+"/keys?status=active&limit=0", nil, &keys, http.StatusOK)
	if len(keys) != 0 || hdr.Get(HeaderTotalCount) != "6" { // j, a and agent-0..3
		t.Fatalf("active key count: %v", hdr)
	}

	// Audit entries carry the names of the keys and machines involved, even
	// after a key is revoked.
	var audit []map[string]any
	h.api(http.MethodGet, base+"/audit?action=key.revoke", nil, &audit, http.StatusOK)
	if len(audit) != 1 || audit[0]["detail"].(map[string]any)["name"] != "agent-4" || audit[0]["actor"] != "admin" {
		t.Fatalf("revoke entry: %v", audit)
	}
	h.api(http.MethodGet, base+"/audit?action=machine.join&limit=1", nil, &audit, http.StatusOK)
	if len(audit) != 1 || audit[0]["machine_name"] != "beta" {
		t.Fatalf("join entry: %v", audit)
	}
}
