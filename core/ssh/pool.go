package ssh

import (
	"context"
	"sync"
	"time"
)

// Status is the state of a server's connection.
type Status string

const (
	StatusDisconnected Status = "disconnected"
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusError        Status = "error"
)

// Resolver returns how to reach the server with an ID.
type Resolver func(id string) (Target, error)

// Pool keeps one connection per server. A lost connection is dialed again
// on the next Get.
type Pool struct {
	resolve   Resolver
	keepalive time.Duration
	onStatus  func(id string, st Status, err error)

	mu      sync.Mutex
	clients map[string]*Client
	dialing map[string]*dialCall
}

type dialCall struct {
	done   chan struct{}
	client *Client
	err    error
}

// NewPool returns a pool that resolves server IDs with resolve. onStatus,
// if not nil, is told when a server's connection changes state.
func NewPool(resolve Resolver, onStatus func(id string, st Status, err error)) *Pool {
	if onStatus == nil {
		onStatus = func(string, Status, error) {}
	}
	return &Pool{
		resolve:   resolve,
		keepalive: 15 * time.Second,
		onStatus:  onStatus,
		clients:   map[string]*Client{},
		dialing:   map[string]*dialCall{},
	}
}

// Get returns the connection to a server, dialing it when there is none or
// it was lost. Concurrent calls share one dial.
func (p *Pool) Get(ctx context.Context, id string) (*Client, error) {
	p.mu.Lock()
	if c := p.clients[id]; c != nil && c.Alive() {
		p.mu.Unlock()
		return c, nil
	}
	if call := p.dialing[id]; call != nil {
		p.mu.Unlock()
		select {
		case <-call.done:
			return call.client, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &dialCall{done: make(chan struct{})}
	p.dialing[id] = call
	p.mu.Unlock()

	p.onStatus(id, StatusConnecting, nil)
	call.client, call.err = p.dial(id)

	p.mu.Lock()
	delete(p.dialing, id)
	if call.err == nil {
		p.clients[id] = call.client
	}
	p.mu.Unlock()
	close(call.done)

	if call.err != nil {
		p.onStatus(id, StatusError, call.err)
		return nil, call.err
	}
	p.onStatus(id, StatusConnected, nil)
	c := call.client
	go c.keepalive(p.keepalive)
	go func() {
		<-c.Done()
		p.mu.Lock()
		current := p.clients[id] == c
		if current {
			delete(p.clients, id)
		}
		p.mu.Unlock()
		if current {
			p.onStatus(id, StatusDisconnected, nil)
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return c, nil
	}
}

// dial runs with its own deadline rather than the caller's context: other
// callers may be waiting on the same dial.
func (p *Pool) dial(id string) (*Client, error) {
	t, err := p.resolve(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return Dial(ctx, t)
}

// Status returns the state of a server's connection.
func (p *Pool) Status(id string) Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dialing[id] != nil {
		return StatusConnecting
	}
	if c := p.clients[id]; c != nil && c.Alive() {
		return StatusConnected
	}
	return StatusDisconnected
}

// Close closes the connection to a server, e.g. after its settings changed.
func (p *Pool) Close(id string) {
	p.mu.Lock()
	c := p.clients[id]
	delete(p.clients, id)
	p.mu.Unlock()
	if c != nil {
		c.Close()
		p.onStatus(id, StatusDisconnected, nil)
	}
}

// CloseAll closes every connection.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	clients := p.clients
	p.clients = map[string]*Client{}
	p.mu.Unlock()
	for _, c := range clients {
		c.Close()
	}
}
