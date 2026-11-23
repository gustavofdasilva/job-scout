package analyzer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// gateway simula o OmniRoute: recusa response_format com 400 quando
// rejectJSONMode é true e conta quantas chamadas recebeu com o campo.
func gateway(t *testing.T, rejectJSONMode bool, withFormat *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.ResponseFormat != nil {
			withFormat.Add(1)
			if rejectJSONMode {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":{"message":"response_format not supported"}}`))
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": validJSON}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestJSONModeFallbackOn400(t *testing.T) {
	var withFormat atomic.Int32
	srv := gateway(t, true, &withFormat)
	a := NewAnalyzer(srv.URL, "k", "m", true)

	for i := 0; i < 2; i++ {
		if _, err := a.callWithRetry("prompt"); err != nil {
			t.Fatalf("chamada %d: %v", i, err)
		}
	}
	if withFormat.Load() != 1 || a.jsonMode.Load() {
		t.Fatalf("esperava 1 chamada com response_format e o modo desligado; chamadas=%d ligado=%v", withFormat.Load(), a.jsonMode.Load())
	}
}

func TestJSONModeSentWhenAccepted(t *testing.T) {
	var withFormat atomic.Int32
	srv := gateway(t, false, &withFormat)
	a := NewAnalyzer(srv.URL, "k", "m", true)
	if _, err := a.callWithRetry("prompt"); err != nil {
		t.Fatal(err)
	}
	if withFormat.Load() != 1 || !a.jsonMode.Load() {
		t.Fatalf("response_format deveria ter sido enviado e continuar ligado")
	}

	off := NewAnalyzer(srv.URL, "k", "m", false)
	if _, err := off.callWithRetry("prompt"); err != nil {
		t.Fatal(err)
	}
	if withFormat.Load() != 1 {
		t.Fatal("flag desligada não deveria enviar response_format")
	}
}
