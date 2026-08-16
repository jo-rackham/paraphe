package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// bufferBody drains the body into memory so openScope can hold no pooled
// connection while a slow client dribbles it. Here: an oversized body is
// refused with 413, and a normal body is buffered and stays readable.
func TestBufferBodyEnforcesTheLimitAndBuffers(t *testing.T) {
	// oversized: one byte past the ceiling
	big := httptest.NewRequest(http.MethodPost, "/api/x",
		strings.NewReader(strings.Repeat("a", maxBodySize+1)))
	wBig := httptest.NewRecorder()
	if bufferBody(wBig, big) {
		t.Fatal("an oversized body was accepted")
	}
	if wBig.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body answered %d, want 413", wBig.Code)
	}

	// normal: buffered, and the handler behind it still reads the same bytes
	const payload = `{"status":"to_contact"}`
	r := httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader(payload))
	w := httptest.NewRecorder()
	if !bufferBody(w, r) {
		t.Fatalf("a normal body was refused: %d", w.Code)
	}
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("body not re-readable after buffering: %v", err)
	}
	if string(got) != payload {
		t.Errorf("buffered body = %q, want %q", got, payload)
	}
}
