package watchdog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// telegramTextLimit is Telegram's cap on one message.
	telegramTextLimit = 4096
	// telegramTimeout bounds the whole send — the message and every profile
	// together. The notifier runs on the watchdog's own goroutine, so a hung
	// connection would stop it sampling.
	telegramTimeout = 60 * time.Second
)

// telegramBase is the Bot API prefix. A variable so the tests can point the
// sender at their own server.
var telegramBase = "https://api.telegram.org/bot"

// Telegram returns a Notifier that posts the report to a chat and attaches
// each profile as a file, so it can be opened with `go tool pprof` straight
// from the chat.
//
// onError is called when the send fails; pass a logger. It may be nil, but a
// watchdog that cannot reach its chat and says nothing about it is a watchdog
// nobody knows is broken.
//
// This talks to the Bot API over net/http rather than through a client
// library, so adding alerting to a service does not add a dependency to it.
//
// NEVER sends from a test binary. A local test run once posted a panic to a
// team channel, where it was read as a production incident and people went
// looking for an outage that was not there. The check is on the test binary
// rather than on an environment variable, because an environment variable is
// something a test forgets to set.
func Telegram(token string, chatID int64, who string, onError func(error)) Notifier {
	client := &http.Client{Timeout: telegramTimeout}

	return func(ev Event) {
		if flag.Lookup("test.v") != nil {
			return
		}

		if token == "" || chatID == 0 {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), telegramTimeout)
		defer cancel()

		if err := sendTelegram(ctx, client, token, chatID, who, ev); err != nil && onError != nil {
			onError(err)
		}
	}
}

// Notify runs several notifiers in turn, so an event can go to the log and to
// a chat without the caller writing the plumbing.
func Notify(fs ...Notifier) Notifier {
	return func(ev Event) {
		for _, f := range fs {
			if f != nil {
				f(ev)
			}
		}
	}
}

func sendTelegram(ctx context.Context, c *http.Client, token string, chatID int64, who string, ev Event) error {
	text := ev.Report(who)
	if len(text) > telegramTextLimit {
		text = text[:telegramTextLimit-1] + "…"
	}

	if err := telegramMessage(ctx, c, token, chatID, text); err != nil {
		return err
	}

	stamp := ev.Time.Format("20060102_150405")

	for _, p := range sortedProfiles(ev.Profiles) {
		name := stamp + "_" + p.fileName()
		caption := string(p) + " profile — go tool pprof -top -cum " + name

		if err := telegramDocument(ctx, c, token, chatID, name, caption, ev.Profiles[p]); err != nil {
			return fmt.Errorf("%s profile: %w", p, err)
		}
	}

	return nil
}

func telegramMessage(ctx context.Context, c *http.Client, token string, chatID int64, text string) error {
	body, err := json.Marshal(map[string]any{"chat_id": chatID, "text": text})
	if err != nil {
		return fmt.Errorf("telegram message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		telegramBase+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram message: %w", withoutURL(err))
	}

	req.Header.Set("Content-Type", "application/json")

	return telegramDo(c, req, "message")
}

func telegramDocument(ctx context.Context, c *http.Client, token string, chatID int64,
	name, caption string, data []byte,
) error {
	if len(data) == 0 {
		return nil
	}

	var buf bytes.Buffer

	mw := multipart.NewWriter(&buf)

	for field, value := range map[string]string{
		"chat_id": strconv.FormatInt(chatID, 10),
		"caption": caption,
	} {
		if err := mw.WriteField(field, value); err != nil {
			return fmt.Errorf("telegram document: %w", err)
		}
	}

	part, err := mw.CreateFormFile("document", name)
	if err != nil {
		return fmt.Errorf("telegram document: %w", err)
	}

	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("telegram document: %w", err)
	}

	if err := mw.Close(); err != nil {
		return fmt.Errorf("telegram document: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		telegramBase+token+"/sendDocument", &buf)
	if err != nil {
		return fmt.Errorf("telegram document: %w", withoutURL(err))
	}

	req.Header.Set("Content-Type", mw.FormDataContentType())

	return telegramDo(c, req, "document")
}

func telegramDo(c *http.Client, req *http.Request, what string) error {
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", what, withoutURL(err))
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)

		return nil
	}

	// Telegram explains itself in the body, and the explanation is usually
	// the whole answer: a wrong chat id, a bot not in the group.
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	return fmt.Errorf("telegram %s: %s: %s", what, resp.Status, bytes.TrimSpace(detail))
}

func sortedProfiles(m map[Profile][]byte) []Profile {
	out := make([]Profile, 0, len(m))
	for p := range m {
		out = append(out, p)
	}

	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}

	return out
}

// withoutURL drops the request URL from a transport error: the bot token is
// in the path, and these errors end up in logs.
func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}

	return err
}
