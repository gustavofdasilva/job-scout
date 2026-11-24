package notifier

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "123456:SECRET-token"

// fakeTelegram responde cada chamada com handlers[n] (a última se repete)
// e conta as chamadas.
func fakeTelegram(t *testing.T, handlers ...http.HandlerFunc) (*Telegram, *atomic.Int32, *[]time.Duration) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		handlers[min(n, len(handlers)-1)](w, r)
	}))
	t.Cleanup(srv.Close)

	var waits []time.Duration
	tg := NewTelegram(testToken, "42", 0)
	tg.apiURL = srv.URL
	tg.sleep = func(d time.Duration) { waits = append(waits, d) }
	return tg, &calls, &waits
}

// resetConn fecha a conexão sem responder — o cliente vê um erro de rede,
// como o "connection reset by peer" do log.
func resetConn(w http.ResponseWriter, _ *http.Request) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		conn.Close()
	}
}

func ok(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte(`{"ok":true}`))
}

func TestSendRetriesNetworkError(t *testing.T) {
	tg, calls, waits := fakeTelegram(t, resetConn, ok)
	if err := tg.send("oi"); err != nil {
		t.Fatalf("deveria ter sucesso na segunda tentativa: %v", err)
	}
	if calls.Load() != 2 || len(*waits) != 1 {
		t.Fatalf("chamadas=%d esperas=%v", calls.Load(), *waits)
	}
}

func TestSendHonorsRetryAfter(t *testing.T) {
	tooMany := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"ok":false,"description":"Too Many Requests: retry after 5","parameters":{"retry_after":5}}`))
	}
	tg, _, waits := fakeTelegram(t, tooMany, ok)
	if err := tg.send("oi"); err != nil {
		t.Fatal(err)
	}
	if len(*waits) != 1 || (*waits)[0] != 5*time.Second {
		t.Fatalf("esperava aguardar 5s (retry_after), aguardou %v", *waits)
	}
}

func TestSendDoesNotRetryBadRequest(t *testing.T) {
	bad := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
	}
	tg, calls, _ := fakeTelegram(t, bad)
	err := tg.send("oi")
	if err == nil || !strings.Contains(err.Error(), "chat not found") || calls.Load() != 1 {
		t.Fatalf("400 não deveria ser retentado: chamadas=%d err=%v", calls.Load(), err)
	}
}

func TestSendRedactsToken(t *testing.T) {
	tg, calls, _ := fakeTelegram(t, resetConn)
	err := tg.send("oi")
	if err == nil || calls.Load() != maxSendAttempts {
		t.Fatalf("esperava falha após %d tentativas: chamadas=%d err=%v", maxSendAttempts, calls.Load(), err)
	}
	if strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), "<token>") {
		t.Fatalf("token não foi removido do erro: %v", err)
	}
}
