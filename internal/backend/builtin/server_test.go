package builtin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerModelsAndNotImplementedRoute(t *testing.T) {
	h := Handler(ServeConfig{
		Listen:    "127.0.0.1:18317",
		Model:     "m-test",
		BackendID: "builtin",
	})

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatalf("models request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected models status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "m-test") {
		t.Fatalf("expected model in body: %s", string(body))
	}

	resp2, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("messages request failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotImplemented {
		t.Fatalf("unexpected messages status: %d", resp2.StatusCode)
	}
}

