package watchdog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTelegram records what the Bot API was asked to do.
type fakeTelegram struct {
	srv *httptest.Server

	mu    sync.Mutex
	paths []string
	files []string
	text  string

	status int
	body   string
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()

	f := &fakeTelegram{status: http.StatusOK, body: `{"ok":true}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		f.paths = append(f.paths, r.URL.Path)

		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			b, _ := io.ReadAll(r.Body)
			f.text = string(b)
		}

		if strings.HasSuffix(r.URL.Path, "/sendDocument") {
			if err := r.ParseMultipartForm(8 << 20); err == nil && r.MultipartForm != nil {
				for _, hs := range r.MultipartForm.File {
					for _, h := range hs {
						f.files = append(f.files, h.Filename)
					}
				}
			}
		}

		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	}))
	t.Cleanup(f.srv.Close)

	return f
}

func (f *fakeTelegram) seen() ([]string, []string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.paths...), append([]string(nil), f.files...), f.text
}

func sampleEvent() Event {
	return Event{
		Time:      time.Date(2026, 10, 21, 13, 29, 33, 0, time.UTC),
		Reasons:   []string{"heap 1.51 GiB >= 1.50 GiB"},
		HeapBytes: 1 << 30,
		Profiles: map[Profile][]byte{
			Heap:      []byte("heap-bytes"),
			Goroutine: []byte("goroutine-bytes"),
		},
	}
}

// The report goes out as a message and each profile as a file, named so that
// the caption's pprof command matches what lands in the chat.
func TestSendTelegram(t *testing.T) {
	f := newFakeTelegram(t)

	old := telegramBase
	telegramBase = f.srv.URL + "/bot"

	defer func() { telegramBase = old }()

	if err := sendTelegram(context.Background(), f.srv.Client(), "tok", 42, "doc-service", sampleEvent()); err != nil {
		t.Fatalf("sendTelegram: %v", err)
	}

	paths, files, text := f.seen()

	var msg, docs int

	for _, p := range paths {
		switch {
		case strings.HasSuffix(p, "/sendMessage"):
			msg++
		case strings.HasSuffix(p, "/sendDocument"):
			docs++
		}
	}

	if msg != 1 {
		t.Errorf("%d messages sent, want 1", msg)
	}

	if docs != 2 {
		t.Errorf("%d documents sent, want 2 (heap and goroutine)", docs)
	}

	if !strings.Contains(text, "doc-service") {
		t.Errorf("the message does not name the service: %s", text)
	}

	want := map[string]bool{
		"20261021_132933_heap.pb.gz":      false,
		"20261021_132933_goroutine.pb.gz": false,
	}

	for _, name := range files {
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Errorf("no file named %q; got %v", name, files)
		}
	}
}

// Telegram explains a refusal in the body — a wrong chat id, a bot that is
// not in the group — and that explanation is usually the whole answer.
func TestSendTelegramReportsTheReason(t *testing.T) {
	f := newFakeTelegram(t)
	f.status = http.StatusBadRequest
	f.body = `{"ok":false,"description":"chat not found"}`

	old := telegramBase
	telegramBase = f.srv.URL + "/bot"

	defer func() { telegramBase = old }()

	err := sendTelegram(context.Background(), f.srv.Client(), "tok", 42, "svc", sampleEvent())
	if err == nil {
		t.Fatal("a 400 was reported as success")
	}

	if !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("error %q does not carry Telegram's reason", err)
	}
}

// The bot token is in the request path, and transport errors carry the URL.
// Those errors end up in logs.
func TestWithoutURLHidesTheToken(t *testing.T) {
	const token = "123456:SECRET-TOKEN"

	err := &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot" + token + "/sendMessage",
		Err: errors.New("connection refused"),
	}

	got := withoutURL(err).Error()

	if strings.Contains(got, token) {
		t.Errorf("the token survived into %q", got)
	}

	if !strings.Contains(got, "connection refused") {
		t.Errorf("the cause was lost from %q", got)
	}
}

// Nothing is sent from a test binary. A local run once posted a panic to a
// team channel and people went looking for an outage that was not there.
// This test passing is itself the evidence: it runs inside a test binary.
func TestTelegramNeverSendsFromATest(t *testing.T) {
	f := newFakeTelegram(t)

	old := telegramBase
	telegramBase = f.srv.URL + "/bot"

	defer func() { telegramBase = old }()

	var sawError error

	notify := Telegram("tok", 42, "svc", func(err error) { sawError = err })
	notify(sampleEvent())

	paths, _, _ := f.seen()
	if len(paths) != 0 {
		t.Errorf("a test binary reached Telegram: %v", paths)
	}

	if sawError != nil {
		t.Errorf("unexpected error: %v", sawError)
	}
}

// An unconfigured notifier does nothing rather than failing on every event.
func TestTelegramWithoutCredentials(t *testing.T) {
	for _, tt := range []struct {
		name   string
		token  string
		chatID int64
	}{
		{name: "no token", chatID: 42},
		{name: "no chat", token: "tok"},
		{name: "neither"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var called bool

			Telegram(tt.token, tt.chatID, "svc", func(error) { called = true })(sampleEvent())

			if called {
				t.Error("an unconfigured notifier reported an error")
			}
		})
	}
}

// Notify lets an event go to the log and to a chat without the caller
// writing the plumbing, and a nil notifier in the list is not a crash.
func TestNotifyRunsEachOne(t *testing.T) {
	var a, b int

	Notify(
		func(Event) { a++ },
		nil,
		func(Event) { b++ },
	)(Event{})

	if a != 1 || b != 1 {
		t.Errorf("notifiers ran %d and %d times, want 1 each", a, b)
	}
}
