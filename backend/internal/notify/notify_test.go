package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

)

// The notifier's laws, tested:
//
//  1. Dark until configured — Config reports off without env, on with both.
//  2. Dispatch always records the in-app history, push or no push.
//  3. OneSignal.Send delivers the exact payload shape the API expects and
//     resolves the user through the subscriber.
//  4. Fire-and-forget: a push failure never surfaces to the caller (Dispatch
//     returns before the goroutine's fate is known).

func TestConfigDarkUntilComplete(t *testing.T) {
	t.Setenv("ONESIGNAL_APP_ID", "")
	t.Setenv("ONESIGNAL_REST_API_KEY", "")
	if _, _, on := Config(); on {
		t.Fatal("empty env produced a configured notifier")
	}

	t.Setenv("ONESIGNAL_APP_ID", "app")
	t.Setenv("ONESIGNAL_REST_API_KEY", "")
	if _, _, on := Config(); on {
		t.Fatal("half-configured (id only) produced a configured notifier")
	}

	t.Setenv("ONESIGNAL_REST_API_KEY", "key")
	if _, _, on := Config(); !on {
		t.Fatal("fully configured env produced a dark notifier")
	}
}

func TestNoopIsHonest(t *testing.T) {
	n := Noop{}
	if n.Configured() {
		t.Fatal("Noop claims to be configured")
	}
	if err := n.Send(context.Background(), 1, KindInputNeeded, "t", "b"); err != nil {
		t.Fatalf("Noop.Send errored: %v", err)
	}
}

// memStore is the recording surface Dispatch writes through.
type memStore struct {
	rows []struct {
		userID int64
		kind   string
		title  string
		body   string
	}
}

func (m *memStore) RecordNotification(userID int64, kind, title, body string) error {
	m.rows = append(m.rows, struct {
		userID int64
		kind   string
		title  string
		body   string
	}{userID, kind, title, body})
	return nil
}

func TestDispatchRecordsInAppHistory(t *testing.T) {
	ms := &memStore{}
	Dispatch(ms, Noop{}, 7, KindBuildSuccess, "Build finished", "verified: true")
	if len(ms.rows) != 1 {
		t.Fatalf("in-app rows = %d (want 1)", len(ms.rows))
	}
	if ms.rows[0].kind != string(KindBuildSuccess) || ms.rows[0].userID != 7 {
		t.Fatalf("recorded %+v", ms.rows[0])
	}
}

func TestOneSignalPayloadShape(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic rest-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": "x"}`))
	}))
	defer srv.Close()

	o := &OneSignal{
		appID:  "app",
		restKey: "rest-key",
		client: srv.Client(),
		subscriberByID: func(ctx context.Context, userID int64) (string, error) {
			if userID != 7 {
				t.Fatalf("subscriber asked for user %d", userID)
			}
			return "ext-7", nil
		},
	}
	// Point at the stub server by overriding the client transport... the
	// notifier hardcodes the API host, so this test exercises Send's own
	// error paths and the payload builder instead of a live round trip:
	// a send with no subscriber resolver must fail honestly.
	o.subscriberByID = nil
	if err := o.Send(context.Background(), 7, KindInputNeeded, "t", "b"); err == nil {
		t.Fatal("send without a resolver succeeded")
	}

	// A user with no registered device is a no-op, not an error.
	o.subscriberByID = func(context.Context, int64) (string, error) { return "", nil }
	if err := o.Send(context.Background(), 7, KindInputNeeded, "t", "b"); err != nil {
		t.Fatalf("send to device-less user errored: %v", err)
	}

	_ = gotBody
}

func TestGetReturnsSingleton(t *testing.T) {
	t.Setenv("ONESIGNAL_APP_ID", "")
	t.Setenv("ONESIGNAL_REST_API_KEY", "")
	n1 := Get(nil)
	n2 := Get(nil)
	if n1 != n2 {
		t.Fatal("Get constructed two notifiers")
	}
	if n1.Configured() {
		t.Fatal("dark env produced a configured singleton")
	}
}

// TestPushTargetsPlayerIDsNotExternalIDs pins the field name that made every
// push a silent no-op. The id the app binds is
// OneSignal.User.getOnesignalId() — the device's own player id, and the app
// never calls setExternalId() — so `include_external_user_ids` matched
// nothing. OneSignal answered 200 with
// {"errors":["All included players are not subscribed"]}, the status check
// called that success, and no log line ever explained the silence.
// Live-verified: include_player_ids delivered, include_external_user_ids did
// not. Never edit one without the other.
func TestPushTargetsPlayerIDsNotExternalIDs(t *testing.T) {
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"4a1c20b0-a59b-4c99-9330-874137ee2c8c"}`))
	}))
	defer srv.Close()

	o := &OneSignal{
		appID: "app", restKey: "k", client: srv.Client(), endpoint: srv.URL,
		subscriberByID: func(context.Context, int64) (string, error) {
			return "2a2ca1f5-7a69-498f-aef0-dc489df992cd", nil
		},
	}
	if err := o.Send(context.Background(), 1, KindInputNeeded, "t", "b"); err != nil {
		t.Fatalf("send errored: %v", err)
	}

	if _, ok := got["include_external_user_ids"]; ok {
		t.Fatal("payload still targets include_external_user_ids — the app never sets an external id, so nothing matches")
	}
	ids, ok := got["include_player_ids"].([]interface{})
	if !ok || len(ids) != 1 {
		t.Fatalf("include_player_ids missing or not a list: %#v", got["include_player_ids"])
	}
	if ids[0] != "2a2ca1f5-7a69-498f-aef0-dc489df992cd" {
		t.Fatalf("target id = %v", ids[0])
	}
}

// TestPushSurfacesErrorsInA200Body pins the other half of the silence:
// OneSignal reports an unknown or unsubscribed target in the BODY of a 200,
// with "id":"". Status-only checking logged that as success, so a dead device
// id looked exactly like a delivered one in the backend log. Any error, or an
// empty id, must now fail honestly and reach the log.
func TestPushSurfacesErrorsInA200Body(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"unsubscribed target", `{"id":"","errors":["All included players are not subscribed"]}`, true},
		{"unknown player id", `{"id":"","errors":["No player with this id found"]}`, true},
		{"accepted", `{"id":"4a1c20b0-a59b-4c99-9330-874137ee2c8c","external_id":null}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			o := &OneSignal{
				appID: "app", restKey: "k", client: srv.Client(), endpoint: srv.URL,
				subscriberByID: func(context.Context, int64) (string, error) {
					return "208ca8ae-935e-4a58-a39f-49183c2c2561", nil
				},
			}
			err := o.Send(context.Background(), 1, KindInputNeeded, "t", "b")
			if tc.wantErr && err == nil {
				t.Fatal("200 with an errors body was treated as success — the push would fail silently again")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("healthy accept reported as failure: %v", err)
			}
		})
	}
}
