package askpass

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// Broker accepts prompts from the askpass helpers of one connection attempt
// and relays them, one at a time, to the requesting command.
type Broker struct {
	listener net.Listener
	path     string
	token    string
	relay    Prompter
	ctx      context.Context
	cancel   context.CancelFunc
	relays   sync.Mutex
	waiting  atomic.Int32
	handlers sync.WaitGroup
	done     chan struct{}
	once     sync.Once
}

// NewBroker listens on a new user-only socket in directory.
func NewBroker(ctx context.Context, directory string, relay Prompter) (*Broker, error) {
	name, err := randomHex(6)
	if err != nil {
		return nil, err
	}
	token, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "ap-"+name+".sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen for OpenSSH prompts: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("protect OpenSSH prompt socket: %w", err)
	}
	brokerContext, cancel := context.WithCancel(ctx)
	broker := &Broker{
		listener: listener, path: path, token: token, relay: relay,
		ctx: brokerContext, cancel: cancel, done: make(chan struct{}),
	}
	go broker.accept()
	return broker, nil
}

// Socket is the value for SocketEnv.
func (b *Broker) Socket() string { return b.path }

// Token is the value for TokenEnv.
func (b *Broker) Token() string { return b.token }

// Waiting reports whether a prompt is waiting for the requester's answer.
func (b *Broker) Waiting() bool { return b.waiting.Load() > 0 }

// Close stops accepting prompts and abandons any prompt in progress.
func (b *Broker) Close() error {
	var err error
	b.once.Do(func() {
		b.cancel()
		err = b.listener.Close()
		<-b.done
		b.handlers.Wait()
		if removeErr := os.Remove(b.path); removeErr != nil && !os.IsNotExist(removeErr) && err == nil {
			err = removeErr
		}
	})
	return err
}

func (b *Broker) accept() {
	defer close(b.done)
	for {
		connection, err := b.listener.Accept()
		if err != nil {
			return
		}
		b.handlers.Add(1)
		go func() {
			defer b.handlers.Done()
			defer func() { _ = connection.Close() }()
			go func() {
				<-b.ctx.Done()
				_ = connection.Close()
			}()
			b.handle(connection)
		}()
	}
}

func (b *Broker) handle(connection net.Conn) {
	var request helperRequest
	if err := json.NewDecoder(io.LimitReader(connection, maximumMessageSize)).Decode(&request); err != nil {
		return
	}
	response := helperResponse{}
	if subtle.ConstantTimeCompare([]byte(request.Token), []byte(b.token)) == 1 && b.ctx.Err() == nil {
		b.relays.Lock()
		b.waiting.Add(1)
		answer, err := b.relay(b.ctx, request.Prompt)
		b.waiting.Add(-1)
		b.relays.Unlock()
		response = helperResponse{Answer: answer, OK: err == nil}
	}
	_ = json.NewEncoder(connection).Encode(response)
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate OpenSSH prompt credentials: %w", err)
	}
	return hex.EncodeToString(value), nil
}
