package telegram

import (
	"net/url"
	"strings"
)

const redactedSecret = "***"

// redactedError hides a secret in the message of the wrapped error while
// keeping errors.Is/errors.As working for causes like context.Canceled.
type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.cause }

// redactSecret replaces every occurrence of secret in err's message with ***.
// Telegram puts the bot token into the request URL, and *url.Error prints that
// URL, so every error that leaves the HTTP layer must pass through here.
func redactSecret(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, secret) {
		return err
	}
	cause := err
	if urlErr, ok := err.(*url.Error); ok {
		// Drop the URL-carrying layer so the token cannot be recovered via errors.As.
		cause = urlErr.Err
	}
	return &redactedError{msg: strings.ReplaceAll(msg, secret, redactedSecret), cause: cause}
}
