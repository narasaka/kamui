// Package askpass relays OpenSSH prompts from an ssh process owned by the
// background controller to the terminal of the kamui command that requested
// the connection.
//
// The controller may have been started by another terminal or by the menu bar
// app, so its ssh children cannot rely on a controlling terminal. For an
// interactive request the controller runs ssh with SSH_ASKPASS set to the kamui
// executable and the broker variables below; kamui then acts as the askpass
// helper. The helper forwards each prompt to a per-request Broker, which relays
// it over the request's controller connection to the requesting command.
// Answers are never logged.
package askpass

import (
	"context"
	"errors"
)

// Kind describes how a prompt is answered.
type Kind string

const (
	// Secret input is read without echo. Every typed answer is a secret
	// because prompt text, which can include server-supplied text and key
	// paths, cannot prove that echoing is safe.
	Secret Kind = "secret"
	// Confirm is a yes-or-no question answered by accepting or declining.
	Confirm Kind = "confirm"
	// Notice is informational and needs no answer.
	Notice Kind = "notice"
)

// Prompt is one OpenSSH question.
type Prompt struct {
	Text string `json:"text"`
	Kind Kind   `json:"kind"`
}

// Prompter answers one prompt. It returns ErrDeclined to refuse the prompt
// and any other error to abandon the request.
type Prompter func(context.Context, Prompt) (string, error)

// ErrDeclined reports that the user refused a prompt.
var ErrDeclined = errors.New("prompt declined")

// Environment variables that connect the helper to its broker.
const (
	SocketEnv = "KAMUI_ASKPASS_SOCKET"
	TokenEnv  = "KAMUI_ASKPASS_TOKEN"
)

const maximumMessageSize = 64 << 10

type helperRequest struct {
	Token  string `json:"token"`
	Prompt Prompt `json:"prompt"`
}

type helperResponse struct {
	Answer string `json:"answer,omitempty"`
	OK     bool   `json:"ok"`
}
