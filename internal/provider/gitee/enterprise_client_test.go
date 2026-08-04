package gitee

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xingshizhai/issue-flow/internal/config"
	"github.com/xingshizhai/issue-flow/internal/domain"
)

func TestEnterpriseSetIssueStateByTitle(t *testing.T) {
	t.Parallel()
	var putBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer ent-token" {
			t.Fatalf("auth = %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/42/issues/IK58V2":
			_, _ = w.Write([]byte(`{
				"ident":"IK58V2",
				"issue_type":{"id":7,"title":"缺陷"},
				"issue_state":{"id":1,"title":"修复中"}
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/42/issue_types/7/issue_states":
			_, _ = w.Write([]byte(`{"total_count":2,"data":[
				{"id":1,"title":"修复中"},
				{"id":9,"title":"已修复"}
			]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/42/issues/IK58V2":
			if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"ident":"IK58V2","issue_state":{"id":9,"title":"已修复"}}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	client := NewEnterpriseClient("ent-token", server.URL, 42, "", time.Second)
	if err := client.SetIssueStateByTitle(context.Background(), "IK58V2", "已修复"); err != nil {
		t.Fatal(err)
	}
	if putBody["qt"] != "ident" {
		t.Fatalf("payload = %#v", putBody)
	}
	if id, ok := putBody["issue_state_id"].(float64); !ok || int(id) != 9 {
		t.Fatalf("issue_state_id = %#v", putBody["issue_state_id"])
	}
}

func TestEnterpriseNativeStateUsesConfiguredTitles(t *testing.T) {
	t.Parallel()
	var syncedTitle string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/42/issues/IK58V2":
			_, _ = w.Write([]byte(`{
				"ident":"IK58V2",
				"issue_type":{"id":7,"title":"缺陷"},
				"issue_state":{"id":1,"title":"修复中"}
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/42/issue_types/7/issue_states":
			_, _ = w.Write([]byte(`{"data":[{"id":9,"title":"已修复"}]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/42/issues/IK58V2":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			syncedTitle = "已修复"
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	client := NewEnterpriseClient("ent-token", server.URL, 42, "", time.Second)
	workflow := config.Workflow{
		EnterpriseStates: map[string]string{"review": "已修复"},
	}
	syncer := &enterpriseNativeState{
		client:   client,
		workflow: &workflow,
	}
	if err := syncer.Sync(context.Background(), "IK58V2", domain.StateReview); err != nil {
		t.Fatal(err)
	}
	if syncedTitle != "已修复" {
		t.Fatalf("syncedTitle = %q", syncedTitle)
	}
	if err := syncer.Sync(context.Background(), "IK58V2", domain.StateWorking); err != nil {
		t.Fatal(err)
	}
}
