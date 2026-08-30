package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestOpenAIListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[`+
			`{"id":"m1","object":"model","created":1700000000,"owned_by":"acme"},`+
			`{"id":"m2","object":"model","created":0,"owned_by":""}`+
			`]}`)
	}))
	defer srv.Close()

	for _, p := range []ModelLister{
		NewOpenAIProvider("key", srv.URL+"/v1"),
		NewOpenAIResponsesProvider("key", srv.URL+"/v1"),
	} {
		models, err := p.ListModels(context.Background())
		if err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		if len(models) != 2 {
			t.Fatalf("got %d models, want 2", len(models))
		}
		if models[0].ID != "m1" || models[0].OwnedBy != "acme" {
			t.Errorf("models[0] = %+v", models[0])
		}
		if want := time.Unix(1700000000, 0).UTC(); !models[0].Created.Equal(want) {
			t.Errorf("Created = %v, want %v", models[0].Created, want)
		}
		if models[1].ID != "m2" || !models[1].Created.IsZero() {
			t.Errorf("models[1] = %+v", models[1])
		}
	}
}

func TestAnthropicListModelsPagination(t *testing.T) {
	var mu sync.Mutex
	var afterIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if got := r.Header.Get("x-api-key"); got != "key" {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "1000" {
			t.Errorf("limit = %q", got)
		}
		mu.Lock()
		afterID := r.URL.Query().Get("after_id")
		afterIDs = append(afterIDs, afterID)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if afterID == "" {
			fmt.Fprint(w, `{"data":[{"type":"model","id":"claude-a","display_name":"Claude A","created_at":"2025-01-02T03:04:05Z"}],"has_more":true,"first_id":"claude-a","last_id":"claude-a"}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"type":"model","id":"claude-b","display_name":"Claude B","created_at":"2025-02-03T04:05:06Z"}],"has_more":false,"first_id":"claude-b","last_id":"claude-b"}`)
	}))
	defer srv.Close()

	p := NewAnthropicProvider("key", srv.URL)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].ID != "claude-a" || models[0].DisplayName != "Claude A" || models[1].ID != "claude-b" {
		t.Errorf("models = %+v", models)
	}
	if want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC); !models[0].Created.Equal(want) {
		t.Errorf("Created = %v, want %v", models[0].Created, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(afterIDs) != 2 || afterIDs[0] != "" || afterIDs[1] != "claude-a" {
		t.Errorf("after_id sequence = %v, want [\"\" \"claude-a\"]", afterIDs)
	}
}

func TestAnthropicListModelsV1BaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[],"has_more":false}`)
	}))
	defer srv.Close()

	// A base URL already ending in /v1 must not double the prefix.
	p := NewAnthropicProvider("key", srv.URL+"/v1")
	if _, err := p.ListModels(context.Background()); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
}

func TestListModelsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"type":"authentication_error","message":"bad key"}}`)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("key", srv.URL+"/v1", WithRetries(0))
	_, err := p.ListModels(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
}
