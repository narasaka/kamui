package askpass_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/askpass"
)

// lineProgram plays the program on the other end of the JSON lines protocol.
type lineProgram struct {
	messages *bufio.Scanner
	replies  *io.PipeWriter
}

func newLinePrompter(t *testing.T) (askpass.Prompter, lineProgram) {
	t.Helper()
	answersReader, answersWriter := io.Pipe()
	messagesReader, messagesWriter := io.Pipe()
	t.Cleanup(func() {
		_ = answersWriter.Close()
		_ = messagesReader.Close()
	})
	return askpass.JSONLines(answersReader, messagesWriter), lineProgram{messages: bufio.NewScanner(messagesReader), replies: answersWriter}
}

func (p lineProgram) next(t *testing.T) askpass.LineMessage {
	t.Helper()
	if !p.messages.Scan() {
		t.Fatalf("no message: %v", p.messages.Err())
	}
	var message askpass.LineMessage
	if err := json.Unmarshal(p.messages.Bytes(), &message); err != nil {
		t.Fatalf("decode %q: %v", p.messages.Text(), err)
	}
	return message
}

func (p lineProgram) reply(t *testing.T, reply string) {
	t.Helper()
	if _, err := io.WriteString(p.replies, reply+"\n"); err != nil {
		t.Fatal(err)
	}
}

type promptResult struct {
	answer string
	err    error
}

func ask(prompter askpass.Prompter, ctx context.Context, prompt askpass.Prompt) <-chan promptResult {
	results := make(chan promptResult, 1)
	go func() {
		answer, err := prompter(ctx, prompt)
		results <- promptResult{answer, err}
	}()
	return results
}

func TestJSONLinesRelaysPromptsAndAnswers(t *testing.T) {
	t.Parallel()
	prompter, program := newLinePrompter(t)

	results := ask(prompter, context.Background(), askpass.Prompt{Text: "Enter passphrase for key '/k': ", Kind: askpass.Secret})
	message := program.next(t)
	if message.Prompt == nil || *message.Prompt != (askpass.LinePrompt{ID: 1, Kind: askpass.Secret, Text: "Enter passphrase for key '/k': "}) {
		t.Fatalf("message = %#v", message)
	}
	program.reply(t, `{"id":1,"answer":"open sesame\nextra"}`)
	if result := <-results; result.err != nil || result.answer != "open sesame" {
		t.Fatalf("result = %#v, want first line of the answer", result)
	}

	results = ask(prompter, context.Background(), askpass.Prompt{Text: "Allow use of key?", Kind: askpass.Confirm})
	if message := program.next(t); message.Prompt == nil || message.Prompt.ID != 2 || message.Prompt.Kind != askpass.Confirm {
		t.Fatalf("message = %#v", message)
	}
	program.reply(t, `{"id":2,"declined":true}`)
	if result := <-results; !errors.Is(result.err, askpass.ErrDeclined) {
		t.Fatalf("result = %#v, want declined", result)
	}
}

func TestJSONLinesNoticeNeedsNoReply(t *testing.T) {
	t.Parallel()
	prompter, program := newLinePrompter(t)

	results := ask(prompter, context.Background(), askpass.Prompt{Text: "Confirm user presence for key", Kind: askpass.Notice})
	if message := program.next(t); message.Prompt == nil || message.Prompt.Kind != askpass.Notice {
		t.Fatalf("message = %#v", message)
	}
	select {
	case result := <-results:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notice waited for a reply")
	}
}

func TestJSONLinesCancelsAbandonedPromptAndIgnoresItsLateReply(t *testing.T) {
	t.Parallel()
	prompter, program := newLinePrompter(t)

	ctx, cancel := context.WithCancel(context.Background())
	results := ask(prompter, ctx, askpass.Prompt{Text: "Password: ", Kind: askpass.Secret})
	_ = program.next(t)
	cancel()
	if message := program.next(t); message.Cancel != 1 || message.Prompt != nil {
		t.Fatalf("message = %#v, want cancel of prompt 1", message)
	}
	if result := <-results; !errors.Is(result.err, context.Canceled) {
		t.Fatalf("result = %#v, want cancellation", result)
	}

	results = ask(prompter, context.Background(), askpass.Prompt{Text: "Password: ", Kind: askpass.Secret})
	_ = program.next(t)
	program.reply(t, `{"id":1,"answer":"stale"}`)
	program.reply(t, `{"id":2,"answer":"fresh"}`)
	if result := <-results; result.err != nil || result.answer != "fresh" {
		t.Fatalf("result = %#v, want reply to prompt 2", result)
	}
}

func TestJSONLinesAbandonsPromptWhenInputEndsOrIsMalformed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "closed", input: "", want: askpass.ErrPromptChannelClosed.Error()},
		{name: "malformed", input: "open sesame\n", want: "malformed reply"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output strings.Builder
			prompter := askpass.JSONLines(strings.NewReader(test.input), &output)
			_, err := prompter(context.Background(), askpass.Prompt{Text: "Password: ", Kind: askpass.Secret})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "open sesame") {
				t.Fatalf("error %q repeats the input", err)
			}
		})
	}
}
