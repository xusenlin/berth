package deploy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/ssh"
)

// Check runs an app's health check on the server's loopback, so a passing
// check with an unreachable port points at the firewall, not the app.
func Check(ctx context.Context, m *app.Manager, a app.App) error {
	timeout := time.Duration(a.Health.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	before, err := m.Status(ctx, a.Name)
	if err != nil {
		return err
	}
	switch a.Health.Type {
	case app.HealthNone:
		return nil
	case app.HealthProcess, "":
		return checkProcess(ctx, m, a, before.Restarts)
	case app.HealthTCP:
		return poll(ctx, m, a, before.Restarts, func(ctx context.Context) error { return probeTCP(ctx, m.C, a.Port) })
	case app.HealthHTTP:
		return poll(ctx, m, a, before.Restarts, func(ctx context.Context) error { return probeHTTP(ctx, m.C, a.Port, a.Health.Path) })
	default:
		return fmt.Errorf("未知的健康检查类型 %q", a.Health.Type)
	}
}

// alive fails when the unit stopped or restarted since the check began.
func alive(ctx context.Context, m *app.Manager, name string, restarts int) error {
	st, err := m.Status(ctx, name)
	if err != nil {
		return err
	}
	if st.Restarts > restarts {
		return errors.New("进程启动后退出，已被 systemd 重启")
	}
	if !st.Running() {
		return fmt.Errorf("进程未在运行（%s/%s）", st.Active, st.Sub)
	}
	return nil
}

// checkProcess passes when the unit stays up for the whole timeout.
func checkProcess(ctx context.Context, m *app.Manager, a app.App, restarts int) error {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		// The check's own deadline is the success condition: use a fresh
		// context so the last status read is not cut short.
		if err := alive(context.WithoutCancel(ctx), m, a.Name, restarts); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return alive(context.WithoutCancel(ctx), m, a.Name, restarts)
			}
			return ctx.Err()
		case <-t.C:
		}
	}
}

// poll runs probe until it passes, the unit dies, or ctx ends.
func poll(ctx context.Context, m *app.Manager, a app.App, restarts int, probe func(context.Context) error) error {
	var last error
	for {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		last = probe(pctx)
		cancel()
		if last == nil {
			return nil
		}
		if err := alive(context.WithoutCancel(ctx), m, a.Name, restarts); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%d 秒内未通过健康检查：%w", a.Health.Timeout, last)
			}
			return ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
	}
}

func loopback(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

// forwardingDisabled reports whether sshd refused a direct-tcpip channel
// (AllowTcpForwarding no), as opposed to the port being closed.
func forwardingDisabled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "administratively prohibited")
}

func probeTCP(ctx context.Context, c *ssh.Client, port int) error {
	conn, err := c.DialRemote(ctx, loopback(port))
	if err == nil {
		conn.Close()
		return nil
	}
	if !forwardingDisabled(err) {
		return fmt.Errorf("端口 %d 无法连接", port)
	}
	cmd := fmt.Sprintf("timeout 3 bash -c '</dev/tcp/127.0.0.1/%d' 2>/dev/null || nc -z -w 3 127.0.0.1 %d", port, port)
	if _, err := c.Output(ctx, cmd); err != nil {
		return fmt.Errorf("端口 %d 无法连接", port)
	}
	return nil
}

func probeHTTP(ctx context.Context, c *ssh.Client, port int, path string) error {
	url := "http://" + loopback(port) + path
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return c.DialRemote(ctx, addr) },
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		if forwardingDisabled(err) {
			return probeHTTPRemote(ctx, c, url)
		}
		return fmt.Errorf("请求 %s 失败", url)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s 返回 %d", url, resp.StatusCode)
	}
	return nil
}

func probeHTTPRemote(ctx context.Context, c *ssh.Client, url string) error {
	q := ssh.Quote(url)
	cmd := "if command -v curl >/dev/null; then curl -fsS -o /dev/null -m 3 " + q + "; else wget -q -O /dev/null -T 3 " + q + "; fi"
	if _, err := c.Output(ctx, cmd); err != nil {
		return fmt.Errorf("请求 %s 失败：%w", url, err)
	}
	return nil
}
