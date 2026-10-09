package ssh

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Client is one connection to a server.
type Client struct {
	ssh         *ssh.Client
	fingerprint string
	keyType     string

	mu     sync.Mutex
	sftp   *sftp.Client
	closed chan struct{}
	once   sync.Once
}

func newClient(c *ssh.Client, fingerprint, keyType string) *Client {
	cl := &Client{ssh: c, fingerprint: fingerprint, keyType: keyType, closed: make(chan struct{})}
	go func() {
		_ = c.Wait()
		cl.markClosed()
	}()
	return cl
}

// Fingerprint returns the SHA256 fingerprint of the server's host key.
func (c *Client) Fingerprint() string { return c.fingerprint }

// KeyType returns the type of the server's host key, e.g. ssh-ed25519.
func (c *Client) KeyType() string { return c.keyType }

// Done is closed when the connection is lost or closed.
func (c *Client) Done() <-chan struct{} { return c.closed }

// Alive reports whether the connection is still open.
func (c *Client) Alive() bool {
	select {
	case <-c.closed:
		return false
	default:
		return true
	}
}

// Close closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.sftp != nil {
		c.sftp.Close()
		c.sftp = nil
	}
	c.mu.Unlock()
	err := c.ssh.Close()
	c.markClosed()
	return err
}

func (c *Client) markClosed() { c.once.Do(func() { close(c.closed) }) }

// keepalive sends a request every interval and closes the connection when
// one goes unanswered.
func (c *Client) keepalive(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			done := make(chan error, 1)
			go func() {
				_, _, err := c.ssh.SendRequest("keepalive@openssh.com", true, nil)
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					c.Close()
					return
				}
			case <-time.After(interval):
				c.Close()
				return
			case <-c.closed:
				return
			}
		}
	}
}

// ExitError is a command that exited with a non-zero status.
type ExitError struct {
	Cmd    string
	Status int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("命令退出码 %d", e.Status)
	}
	return msg
}

// Result is the output of a command.
type Result struct {
	Stdout string
	Stderr string
	Status int
}

// Run runs a command with sh and waits for it. stdin may be nil. A
// non-zero exit status is not an error here: see Result.Status.
func (c *Client) Run(ctx context.Context, cmd string, stdin io.Reader) (Result, error) {
	s, err := c.ssh.NewSession()
	if err != nil {
		return Result{}, fmt.Errorf("打开会话：%w", err)
	}
	defer s.Close()
	var stdout, stderr bytes.Buffer
	s.Stdout, s.Stderr, s.Stdin = &stdout, &stderr, stdin
	err = runSession(ctx, s, cmd)
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		res.Status = exit.ExitStatus()
		return res, nil
	}
	return res, err
}

// Output runs a command and returns its standard output, or an *ExitError
// carrying its standard error when it fails.
func (c *Client) Output(ctx context.Context, cmd string) (string, error) {
	res, err := c.Run(ctx, cmd, nil)
	if err != nil {
		return "", err
	}
	if res.Status != 0 {
		return res.Stdout, &ExitError{Cmd: cmd, Status: res.Status, Stderr: res.Stderr}
	}
	return res.Stdout, nil
}

// Stream runs a command and calls line for each line of its standard
// output until it exits or ctx ends. line returning an error stops it.
func (c *Client) Stream(ctx context.Context, cmd string, line func(string) error) error {
	s, err := c.ssh.NewSession()
	if err != nil {
		return fmt.Errorf("打开会话：%w", err)
	}
	defer s.Close()
	out, err := s.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	s.Stderr = &stderr
	if err := s.Start(cmd); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() {
		_ = s.Signal(ssh.SIGTERM)
		s.Close()
	})
	defer stop()

	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var lineErr error
	for sc.Scan() {
		if lineErr = line(sc.Text()); lineErr != nil {
			cancel()
			break
		}
	}
	waitErr := s.Wait()
	switch {
	case lineErr != nil:
		return lineErr
	case ctx.Err() != nil:
		return ctx.Err()
	}
	var exit *ssh.ExitError
	if errors.As(waitErr, &exit) && exit.ExitStatus() != 0 {
		return &ExitError{Cmd: cmd, Status: exit.ExitStatus(), Stderr: stderr.String()}
	}
	return waitErr
}

func runSession(ctx context.Context, s *ssh.Session, cmd string) error {
	if err := s.Start(cmd); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = s.Signal(ssh.SIGTERM)
		s.Close()
		<-done
		return ctx.Err()
	}
}

// SFTP returns the connection's SFTP client, opening it on first use.
func (c *Client) SFTP() (*sftp.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sftp != nil {
		return c.sftp, nil
	}
	sc, err := sftp.NewClient(c.ssh, sftp.UseConcurrentWrites(true))
	if err != nil {
		return nil, fmt.Errorf("打开 SFTP：%w", err)
	}
	c.sftp = sc
	go func() {
		_ = sc.Wait()
		c.mu.Lock()
		if c.sftp == sc {
			c.sftp = nil
		}
		c.mu.Unlock()
	}()
	return sc, nil
}

// DialRemote opens a TCP connection from the server, e.g. to 127.0.0.1:port
// for health checks and tunnels.
func (c *Client) DialRemote(ctx context.Context, addr string) (net.Conn, error) {
	return c.ssh.DialContext(ctx, "tcp", addr)
}

// Quote quotes s for sh.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@%+,", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Pipe runs a command, feeding it stdin (which may be nil) and handing its
// standard output to read while it runs. It returns the exit status and the
// standard error once both the command and read are done. A failing read
// stops the command.
func (c *Client) Pipe(ctx context.Context, cmd string, stdin io.Reader, read func(io.Reader) error) (Result, error) {
	s, err := c.ssh.NewSession()
	if err != nil {
		return Result{}, fmt.Errorf("打开会话：%w", err)
	}
	defer s.Close()
	out, err := s.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	var stderr bytes.Buffer
	s.Stderr, s.Stdin = &stderr, stdin
	if err := s.Start(cmd); err != nil {
		return Result{}, err
	}
	stop := context.AfterFunc(ctx, func() {
		_ = s.Signal(ssh.SIGTERM)
		s.Close()
	})
	defer stop()
	readErr := read(out)
	if readErr != nil {
		_ = s.Signal(ssh.SIGTERM)
		s.Close()
	} else {
		_, _ = io.Copy(io.Discard, out) // let the command finish writing
	}
	waitErr := s.Wait()
	res := Result{Stderr: stderr.String()}
	var exit *ssh.ExitError
	if errors.As(waitErr, &exit) {
		res.Status = exit.ExitStatus()
		waitErr = nil
	}
	switch {
	case readErr != nil:
		return res, readErr
	case ctx.Err() != nil:
		return res, ctx.Err()
	}
	return res, waitErr
}
