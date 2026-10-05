// Package notify is the push-notification surface: OneSignal when
// configured, an honest no-op when not. The product laws that shape it:
//
//   - Dark until configured. ONESIGNAL_APP_ID + ONESIGNAL_REST_API_KEY in
//     env turns it on; anything less and the notifier is a Noop that says
//     so, never a half-configured client that silently drops sends.
//   - Fire-and-forget. A notification is a courtesy nudge; it must never
//     block or fail the product path it rides on. Every send runs in its
//     own goroutine with a hard timeout.
//   - The in-app history is the fallback and the record: every
//     notification the product tried to send lands in the notifications
//     table regardless of what OneSignal did with it.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Kind is the product's notification vocabulary. Each kind names the exact
// moment in the product loop that produced it.
type Kind string

const (
	KindInputNeeded  Kind = "input_needed"  // ask_user pause — the turn waits on a reply
	KindPlanReady    Kind = "plan_ready"    // plan turn finished, awaiting approval
	KindBuildSuccess Kind = "build_success" // build turn finished, verified
	KindBuildFailed  Kind = "build_failed"  // build turn finished, failed
	KindBuildPaused  Kind = "build_paused"  // reaper paused the session
	KindBuildDestroy Kind = "build_destroy" // reaper destroyed the idle session
	KindSignIn       Kind = "sign_in"       // fresh login on a device — theft visibility
)

// Notifier sends one push to one user's devices. Implementations must be
// safe for concurrent use.
type Notifier interface {
	// Send delivers the notification. It returns an error the caller logs;
	// it never panics and never blocks longer than its own timeout.
	Send(ctx context.Context, userID int64, kind Kind, title, body string) error
	// Configured is the honest answer to "is push live on this server".
	Configured() bool
}

// Noop is the dark-mode notifier: it says plainly that nothing is wired.
type Noop struct{}

func (Noop) Send(context.Context, int64, Kind, string, string) error { return nil }
func (Noop) Configured() bool                                        { return false }

// OneSignal delivers through OneSignal's REST API. It holds the app's
// credentials and an HTTP client with a hard timeout.
type OneSignal struct {
	appID   string
	restKey string
	client  *http.Client
	// endpoint overrides the create-notification URL; empty means the real
	// OneSignal host. Tests point it at a stub so Send's HTTP path — the
	// payload field name and the 200-with-errors verdict — is actually
	// exercised rather than skipped past.
	endpoint string
	// subscriberByID resolves a user id to the OneSignal subscription id
	// (external id) — wired by the caller so this package stays
	// store-agnostic.
	subscriberByID func(ctx context.Context, userID int64) (string, error)
}

// Config is the env-read state; On tells whether push is live.
func Config() (appID, restKey string, on bool) {
	appID = os.Getenv("ONESIGNAL_APP_ID")
	restKey = os.Getenv("ONESIGNAL_REST_API_KEY")
	on = appID != "" && restKey != ""
	return
}

// NewOneSignal builds the notifier; nil subscriber means sends resolve no
// user and fail honestly (the caller should not construct it then — Config
// gates that).
func NewOneSignal(appID, restKey string, subscriber func(ctx context.Context, userID int64) (string, error)) *OneSignal {
	return &OneSignal{
		appID:          appID,
		restKey:        restKey,
		client:         &http.Client{Timeout: 10 * time.Second},
		subscriberByID: subscriber,
	}
}

func (o *OneSignal) Configured() bool { return true }

// notifyURL is OneSignal's create-notification endpoint. endpoint wins when
// set (tests stub it); production has no endpoint and uses the real host.
func (o *OneSignal) notifyURL() string {
	if o.endpoint != "" {
		return o.endpoint
	}
	return "https://api.onesignal.com/notifications"
}

// onesignalPayload is the subset of OneSignal's notification body this
// product needs: one device by player id, plain title+body.
//
// include_player_ids, NOT include_external_user_ids: the id the app binds
// is its PUSH SUBSCRIPTION id — OneSignal.User.pushSubscription.getIdAsync()
// — which is what include_player_ids addresses, and the app never calls
// setExternalId(), so include_external_user_ids matches nothing. Two bugs
// shared one symptom (200 with
// {"errors":["All included players are not subscribed"]}): targeting by
// external id, and — G-11 — the app posting the IDENTITY id
// getOnesignalId() instead of the subscription id, which addresses no
// device at all. Send reads that error out of the body (below), and
// lib/push.ts now posts the subscription id (verified live: the identity id
// delivered nothing, the subscription id delivered the notification).
// Delivery is pinned by TestPushTargetsPlayerIDsNotExternalIDs.
type onesignalPayload struct {
	AppID            string            `json:"app_id"`
	IncludePlayerIDs []string          `json:"include_player_ids"`
	Headings         map[string]string `json:"headings,omitempty"`
	Contents         map[string]string `json:"contents"`
}

func (o *OneSignal) Send(ctx context.Context, userID int64, kind Kind, title, body string) error {
	if o.subscriberByID == nil {
		return fmt.Errorf("onesignal: no subscriber resolver wired")
	}
	subID, err := o.subscriberByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("resolve subscriber for user %d: %w", userID, err)
	}
	if subID == "" {
		// The user has no registered device — not an error, just nothing
		// to deliver to. The in-app history still recorded it.
		return nil
	}

	payload, err := json.Marshal(onesignalPayload{
		AppID:            o.appID,
		IncludePlayerIDs: []string{subID},
		Headings:         map[string]string{"en": title},
		Contents:         map[string]string{"en": body},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.notifyURL(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+o.restKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Read the body BEFORE deciding: OneSignal reports an unknown or
	// unsubscribed target in the BODY of a 200 —
	// {"id":"","errors":["All included players are not subscribed"]} — and
	// the old status-only check logged that as success, so every dead push
	// left no trace anywhere. Status first (cheap), then the body verdict.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("onesignal: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var verdict struct {
		ID     string   `json:"id"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &verdict); err == nil {
		if len(verdict.Errors) > 0 {
			return fmt.Errorf("onesignal: %s (target %s)", strings.Join(verdict.Errors, "; "), subID)
		}
		if verdict.ID == "" {
			return fmt.Errorf("onesignal: accepted nothing (empty id), target %s", subID)
		}
	}
	return nil
}

// Dispatch is the product's one send path: record in-app, then push
// fire-and-forget. The goroutine is bounded by the notifier's own client
// timeout; errors are logged, never propagated.
func Dispatch(store NotificationStore, notifier Notifier, userID int64, kind Kind, title, body string) {
	if err := store.RecordNotification(userID, string(kind), title, body); err != nil {
		log.Printf("notify: could not record %s for user %d: %v", kind, userID, err)
	}
	if !notifier.Configured() {
		return // dark: recorded in-app, honestly not pushed
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if err := notifier.Send(ctx, userID, kind, title, body); err != nil {
			log.Printf("notify: %s push to user %d failed: %v", kind, userID, err)
		}
	}()
}

// NotificationStore is the persistence surface Dispatch records through —
// the store package implements it.
type NotificationStore interface {
	RecordNotification(userID int64, kind, title, body string) error
}

// once guards the process-wide notifier construction (env read once).
var (
	notifierOnce sync.Once
	theNotifier  Notifier
)

// Get returns the process notifier: OneSignal when fully configured, the
// honest Noop otherwise. subscriber is the resolver NewOneSignal needs.
func Get(subscriber func(ctx context.Context, userID int64) (string, error)) Notifier {
	notifierOnce.Do(func() {
		appID, restKey, on := Config()
		if on {
			theNotifier = NewOneSignal(appID, restKey, subscriber)
			log.Printf("notify: OneSignal push enabled")
			return
		}
		theNotifier = Noop{}
		log.Printf("notify: OneSignal dark (set ONESIGNAL_APP_ID and ONESIGNAL_REST_API_KEY to enable push)")
	})
	return theNotifier
}
