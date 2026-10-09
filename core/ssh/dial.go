// Package ssh connects to servers: authentication, host key checks, a pool
// of long-lived connections with keepalives, commands and SFTP.
package ssh

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Auth is how to log in.
type Auth string

const (
	AuthPassword Auth = "password"
	AuthKey      Auth = "key"
	AuthAgent    Auth = "agent"
)

// Target is everything needed to open a connection.
type Target struct {
	Host    string
	Port    int
	User    string
	Auth    Auth
	KeyPath string
	// Secret is the password, or the passphrase of an encrypted key.
	Secret string
	// HostKey is the expected SHA256 fingerprint of the server's host key.
	// Empty accepts any key, and the error of Dial is then nil: read the
	// fingerprint from the returned client.
	HostKey string
}

// Addr returns host:port.
func (t Target) Addr() string {
	port := t.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(port))
}

// HostKeyError is returned when the server's host key is not the one
// recorded.
type HostKeyError struct {
	Expected string
	Got      string
}

func (e *HostKeyError) Error() string {
	return fmt.Sprintf("主机密钥不匹配：记录的是 %s，服务器提供的是 %s。服务器可能被重装，也可能存在中间人攻击", e.Expected, e.Got)
}

// ErrKeyPassphrase is returned when the private key is encrypted and no
// passphrase, or a wrong one, was given.
var ErrKeyPassphrase = errors.New("私钥已加密，请填写正确的私钥密码")

// Dial opens a connection to the target.
func Dial(ctx context.Context, t Target) (*Client, error) {
	methods, closeAgent, err := authMethods(t)
	if err != nil {
		return nil, err
	}
	defer closeAgent()

	var fingerprint, keyType string
	cfg := &ssh.ClientConfig{
		User: t.User,
		Auth: methods,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fingerprint = ssh.FingerprintSHA256(key)
			keyType = key.Type()
			if t.HostKey != "" && t.HostKey != fingerprint {
				return &HostKeyError{Expected: t.HostKey, Got: fingerprint}
			}
			return nil
		},
		Timeout: 15 * time.Second,
	}

	d := net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", t.Addr())
	if err != nil {
		return nil, fmt.Errorf("无法连接 %s：%w", t.Addr(), err)
	}
	// The handshake has no context: bound it with a deadline, and close the
	// connection if ctx ends first.
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	c, chans, reqs, err := ssh.NewClientConn(conn, t.Addr(), cfg)
	stop()
	if err != nil {
		conn.Close()
		var hk *HostKeyError
		if errors.As(err, &hk) {
			return nil, hk
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, fmt.Errorf("认证失败，请检查用户名和%s", authName(t.Auth))
		}
		return nil, fmt.Errorf("SSH 握手失败：%w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	return newClient(ssh.NewClient(c, chans, reqs), fingerprint, keyType), nil
}

func authName(a Auth) string {
	switch a {
	case AuthPassword:
		return "密码"
	case AuthKey:
		return "私钥"
	default:
		return "ssh-agent 中的密钥"
	}
}

func authMethods(t Target) ([]ssh.AuthMethod, func(), error) {
	noop := func() {}
	switch t.Auth {
	case AuthPassword:
		pw := t.Secret
		return []ssh.AuthMethod{
			ssh.Password(pw),
			// Servers that disable "password" often still accept it through
			// keyboard-interactive.
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}),
		}, noop, nil
	case AuthKey:
		signer, err := loadKey(t.KeyPath, t.Secret)
		if err != nil {
			return nil, noop, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, noop, nil
	case AuthAgent:
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, noop, errors.New("未找到 ssh-agent（SSH_AUTH_SOCK 未设置）")
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, noop, fmt.Errorf("无法连接 ssh-agent：%w", err)
		}
		ag := agent.NewClient(conn)
		return []ssh.AuthMethod{ssh.PublicKeysCallback(ag.Signers)}, func() { conn.Close() }, nil
	default:
		return nil, noop, fmt.Errorf("未知的认证方式 %q", t.Auth)
	}
}

func loadKey(path, passphrase string) (ssh.Signer, error) {
	path = ExpandHome(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取私钥：%w", err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if passphrase == "" {
			return nil, ErrKeyPassphrase
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		if errors.Is(err, x509.IncorrectPasswordError) {
			return nil, ErrKeyPassphrase
		}
	}
	if err != nil {
		return nil, fmt.Errorf("解析私钥：%w", err)
	}
	return signer, nil
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
