package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleModels(t *testing.T) {
	server := NewServer(nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", methodOnly(http.MethodGet, server.handleModels))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if payload["object"] != "list" {
		t.Fatalf("expected object=list, got %v", payload["object"])
	}

	data, ok := payload["data"].([]interface{})
	if !ok || len(data) != 1 {
		t.Fatalf("expected one model in data, got %v", payload["data"])
	}

	model, ok := data[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected model object, got %T", data[0])
	}

	if model["id"] != "DOUBAO" {
		t.Fatalf("expected model id DOUBAO, got %v", model["id"])
	}
}

func TestHandleModelsMethodNotAllowed(t *testing.T) {
	server := NewServer(nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", methodOnly(http.MethodGet, server.handleModels))

	req := httptest.NewRequest(http.MethodPost, "/v1/models", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}
