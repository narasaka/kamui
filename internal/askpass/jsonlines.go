package askpass

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// LinePrompt is one OpenSSH prompt in the JSON lines protocol that
// `kamui mirror --json-prompts` speaks to a program such as the menu bar app.
type LinePrompt struct {
	ID   int    `json:"id"`
	Kind Kind   `json:"kind"`
	Text string `json:"text"`
}

// LineMessage is one line that the CLI writes. Exactly one field is set.
// Cancel names a prompt that no longer needs an answer because its connection
// attempt ended.
type LineMessage struct {
	Prompt *LinePrompt `json:"prompt,omitempty"`
	Cancel int         `json:"cancel,omitempty"`
}

// LineReply is one line that the answering program writes. Declined refuses
// the prompt; otherwise Answer is the reply. Notices need no reply.
type LineReply struct {
	ID       int    `json:"id"`
	Answer   string `json:"answer,omitempty"`
	Declined bool   `json:"declined,omitempty"`
}

// ErrPromptChannelClosed reports that the answering program stopped reading
// or writing prompts, which abandons the request.
var ErrPromptChannelClosed = errors.New("the program answering OpenSSH prompts closed its input")

// JSONLines returns a Prompter for a program that answers OpenSSH prompts on
// the user's behalf without a terminal. It writes each prompt to out as one
// JSON LineMessage and reads LineReply values from in, one per line. Input is
// read only once a prompt needs an answer. Answers are never logged; a line
// break ends an answer, as it does on a terminal.
func JSONLines(in io.Reader, out io.Writer) Prompter {
	relay := &lineRelay{in: in, encoder: json.NewEncoder(out), replies: make(chan LineReply)}
	return relay.prompt
}

type lineRelay struct {
	in      io.Reader
	encoder *json.Encoder
	start   sync.Once
	replies chan LineReply
	readErr error
	// prompts serializes prompts and protects next and encoder.
	prompts sync.Mutex
	next    int
}

func (r *lineRelay) prompt(ctx context.Context, prompt Prompt) (string, error) {
	r.prompts.Lock()
	defer r.prompts.Unlock()
	r.next++
	id := r.next
	if err := r.encoder.Encode(LineMessage{Prompt: &LinePrompt{ID: id, Kind: prompt.Kind, Text: prompt.Text}}); err != nil {
		return "", fmt.Errorf("send OpenSSH prompt: %w", err)
	}
	if prompt.Kind == Notice {
		return "", nil
	}
	r.start.Do(func() { go r.read() })
	for {
		select {
		case <-ctx.Done():
			_ = r.encoder.Encode(LineMessage{Cancel: id})
			return "", ctx.Err()
		case reply, ok := <-r.replies:
			if !ok {
				return "", r.readErr
			}
			if reply.ID != id {
				// A reply to an earlier, cancelled prompt.
				continue
			}
			if reply.Declined {
				return "", ErrDeclined
			}
			answer, _, _ := strings.Cut(reply.Answer, "\n")
			return strings.TrimRight(answer, "\r"), nil
		}
	}
}

// read decodes replies until the input ends. A malformed or oversized line
// ends the channel rather than guessing at an answer.
func (r *lineRelay) read() {
	scanner := bufio.NewScanner(r.in)
	scanner.Buffer(make([]byte, 0, 4096), maximumMessageSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var reply LineReply
		if err := json.Unmarshal(line, &reply); err != nil {
			r.readErr = fmt.Errorf("read OpenSSH prompt answer: malformed reply")
			close(r.replies)
			return
		}
		r.replies <- reply
	}
	r.readErr = ErrPromptChannelClosed
	if err := scanner.Err(); err != nil {
		r.readErr = fmt.Errorf("read OpenSSH prompt answer: %w", err)
	}
	close(r.replies)
}
