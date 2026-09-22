package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// W7.2 gate — vision_model rides the wire: with the router on and
// vision_model set, the device gate's vision check hits the fake gateway
// with that model in the request body; with the router off it is the chat
// model, exactly as before — even when vision_model is configured.
func TestVisionGateRoutesToVisionModel(t *testing.T) {
	var mu sync.Mutex
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		models = append(models, body.Model)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": `{"matches":true,"reason":"home screen as intended","has_error":false}`,
				},
			}},
		})
	}))
	defer srv.Close()

	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", srv.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "chat-model")

	lastModel := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(models) == 0 {
			return ""
		}
		return models[len(models)-1]
	}

	t.Run("router_on_uses_vision_model", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_ROUTER", "1")
		t.Setenv("ROUTING_POLICY_JSON", `{"vision_model":"vision-model"}`)
		c, err := ai.New()
		if err != nil {
			t.Fatalf("ai.New: %v", err)
		}
		g := gateVision{c: ai.ForVision(c), target: "emulator-5554"}
		res, err := g.VisionCheck(context.Background(), []byte("fake-png-bytes"), "the home screen")
		if err != nil {
			t.Fatalf("VisionCheck: %v", err)
		}
		if !res.Matches {
			t.Errorf("expected the fake verdict to parse, got %+v", res)
		}
		if got := lastModel(); got != "vision-model" {
			t.Errorf("expected body.model vision-model, got %q", got)
		}
	})

	t.Run("router_off_rides_chat_model", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_ROUTER", "0")
		t.Setenv("ROUTING_POLICY_JSON", `{"vision_model":"vision-model"}`)
		c, err := ai.New()
		if err != nil {
			t.Fatalf("ai.New: %v", err)
		}
		g := gateVision{c: ai.ForVision(c), target: "emulator-5554"}
		if _, err := g.VisionCheck(context.Background(), []byte("fake-png-bytes"), "the home screen"); err != nil {
			t.Fatalf("VisionCheck: %v", err)
		}
		if got := lastModel(); got != "chat-model" {
			t.Errorf("flag off must ride the chat model, got %q", got)
		}
	})
}
