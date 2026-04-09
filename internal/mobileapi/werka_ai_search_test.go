package mobileapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWerkaAISearchServiceInferSuggestionNormalizesBrandQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {
					"parts": [{
						"text": "{\"search_query\":\"Nivea Creme Care\",\"alt_query\":\"Nivea\",\"visible_brand\":\"Nivea\",\"visible_text\":\"Nivea Creme Care\",\"confidence\":0.91}"
					}]
				}
			}]
		}`))
	}))
	defer server.Close()

	service := &werkaAISearchService{
		client:   server.Client(),
		apiKey:   "test-key",
		model:    "test-model",
		endpoint: server.URL,
	}

	got, err := service.inferSuggestion(
		context.Background(),
		[]byte("fake-image"),
		"image/jpeg",
	)
	if err != nil {
		t.Fatalf("inferSuggestion returned error: %v", err)
	}
	if got.DisplayQuery != "nivea" {
		t.Fatalf("unexpected display query: %q", got.DisplayQuery)
	}
	if len(got.BackgroundQueries) == 0 || got.BackgroundQueries[0] != "nivea" {
		t.Fatalf("unexpected background queries: %#v", got.BackgroundQueries)
	}
	if got.VisibleText != "Nivea Creme Care" {
		t.Fatalf("unexpected visible text: %q", got.VisibleText)
	}
}

func TestWerkaAISearchServiceInferSuggestionReturnsNoResult(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[]}`))
	}))
	defer server.Close()

	service := &werkaAISearchService{
		client:   server.Client(),
		apiKey:   "test-key",
		model:    "test-model",
		endpoint: server.URL,
	}

	_, err := service.inferSuggestion(
		context.Background(),
		[]byte("fake-image"),
		"image/jpeg",
	)
	if err == nil {
		t.Fatal("expected no-result error")
	}
	aiErr, ok := err.(*werkaAISearchError)
	if !ok {
		t.Fatalf("expected werkaAISearchError, got %T", err)
	}
	if aiErr.Code != "no_result" {
		t.Fatalf("unexpected error code: %q", aiErr.Code)
	}
}

func TestNewWerkaAISearchServiceDefaultsModel(t *testing.T) {
	t.Parallel()

	service := newWerkaAISearchService("key", "", 0)
	if service.model != werkaAISearchDefaultModel {
		t.Fatalf("unexpected model: %q", service.model)
	}
	if service.client.Timeout != 15*time.Second {
		t.Fatalf("unexpected timeout: %s", service.client.Timeout)
	}
}
