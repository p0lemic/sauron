package alerts

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Notifier is called once when a new alert is first created.
type Notifier interface {
	Notify(a Alert)
}

// WebhookNotifier sends Alert payloads via HTTP POST.
type WebhookNotifier struct {
	URL    string
	Client *http.Client
}

// NewWebhookNotifier creates a WebhookNotifier with a 5-second timeout.
func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		URL:    url,
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify marshals a and POSTs it to w.URL. Errors are logged and swallowed.
func (w *WebhookNotifier) Notify(a Alert) {
	body, err := json.Marshal(a)
	if err != nil {
		log.Printf("webhook: marshal error: %v", err)
		return
	}
	resp, err := w.Client.Post(w.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("webhook: POST %s failed: %v", w.URL, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("webhook: POST %s returned %s", w.URL, resp.Status)
	}
}
