package askpass

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// RunHelper is kamui's behavior when OpenSSH runs it as SSH_ASKPASS with the
// prompt as its argument. It prints the answer for OpenSSH and returns the
// exit status.
func RunHelper(arguments []string, getenv func(string) string, stdout io.Writer) int {
	socket, token := getenv(SocketEnv), getenv(TokenEnv)
	if socket == "" || token == "" {
		return 1
	}
	text := ""
	if len(arguments) > 0 {
		text = arguments[0]
	}
	prompt := Prompt{Text: text, Kind: kindFor(getenv("SSH_ASKPASS_PROMPT"))}
	connection, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return 1
	}
	defer func() { _ = connection.Close() }()
	if err := json.NewEncoder(connection).Encode(helperRequest{Token: token, Prompt: prompt}); err != nil {
		return 1
	}
	var response helperResponse
	if err := json.NewDecoder(io.LimitReader(connection, maximumMessageSize)).Decode(&response); err != nil || !response.OK {
		return 1
	}
	switch prompt.Kind {
	case Notice:
		return 0
	case Confirm:
		_, err = fmt.Fprintln(stdout, "yes")
	default:
		_, err = fmt.Fprintln(stdout, response.Answer)
	}
	if err != nil {
		return 1
	}
	return 0
}

// kindFor classifies a prompt from OpenSSH's SSH_ASKPASS_PROMPT hint.
func kindFor(hint string) Kind {
	switch hint {
	case "confirm":
		return Confirm
	case "none":
		return Notice
	default:
		return Secret
	}
}
