package okx

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Network routing for hosts where OKX domains are DNS-polluted and a local HTTP proxy would
// otherwise route them DIRECT (observed on the home NAS): resolve the real address via DoH and
// CONNECT to the IP through the proxy. TLS still verifies the original hostname (SNI unchanged).

type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

var (
	dialMu     sync.RWMutex
	dialFn     DialFunc // nil = default net.Dialer
	dnsCache   = map[string]dnsEntry{}
	dnsMu      sync.Mutex
	baseDialer = &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
)

type dnsEntry struct {
	ips []string
	at  time.Time
}

// ConfigureNetwork installs the dialer used by REST and WS. proxy may be nil; doh enables
// DNS-over-HTTPS (Cloudflare 1.1.1.1, reached through the same proxy by IP).
func ConfigureNetwork(proxy *url.URL, doh bool) {
	if proxy == nil && !doh {
		return
	}
	d := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		target := addr
		if doh && net.ParseIP(host) == nil {
			ips, err := resolveDoH(ctx, host, proxy)
			if err != nil {
				return nil, fmt.Errorf("doh %s: %w", host, err)
			}
			target = net.JoinHostPort(ips[int(time.Now().UnixNano())%len(ips)], port)
		}
		if proxy == nil {
			return baseDialer.DialContext(ctx, network, target)
		}
		return connectVia(ctx, proxy, target)
	}
	dialMu.Lock()
	dialFn = d
	dialMu.Unlock()
}

func currentDial() DialFunc {
	dialMu.RLock()
	defer dialMu.RUnlock()
	return dialFn
}

// connectVia opens an HTTP CONNECT tunnel to target through proxy.
func connectVia(ctx context.Context, proxy *url.URL, target string) (net.Conn, error) {
	conn, err := baseDialer.DialContext(ctx, "tcp", proxy.Host)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(15 * time.Second))
	}
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if u := proxy.User; u != nil {
		pw, _ := u.Password()
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(u.Username()+":"+pw)) + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s: %s", target, resp.Status)
	}
	if br.Buffered() > 0 { // server spoke first; should not happen before our TLS hello
		conn.Close()
		return nil, fmt.Errorf("proxy sent unexpected bytes after CONNECT")
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

func resolveDoH(ctx context.Context, host string, proxy *url.URL) ([]string, error) {
	dnsMu.Lock()
	if e, ok := dnsCache[host]; ok && time.Since(e.at) < 10*time.Minute {
		dnsMu.Unlock()
		return e.ips, nil
	}
	dnsMu.Unlock()
	tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if proxy == nil {
			return baseDialer.DialContext(ctx, network, addr)
		}
		return connectVia(ctx, proxy, addr)
	}, TLSHandshakeTimeout: 10 * time.Second}
	cl := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://1.1.1.1/dns-query?type=A&name="+url.QueryEscape(host), nil)
	req.Header.Set("Accept", "application/dns-json")
	resp, err := cl.Do(req)
	if err != nil {
		return staleOr(host, err)
	}
	defer resp.Body.Close()
	var body struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return staleOr(host, err)
	}
	var ips []string
	for _, a := range body.Answer {
		if a.Type == 1 && net.ParseIP(a.Data) != nil {
			ips = append(ips, a.Data)
		}
	}
	if len(ips) == 0 {
		return staleOr(host, fmt.Errorf("no A records"))
	}
	dnsMu.Lock()
	dnsCache[host] = dnsEntry{ips, time.Now()}
	dnsMu.Unlock()
	return ips, nil
}

// staleOr serves an expired cache entry rather than failing when DoH is briefly unavailable.
func staleOr(host string, err error) ([]string, error) {
	dnsMu.Lock()
	defer dnsMu.Unlock()
	if e, ok := dnsCache[host]; ok {
		return e.ips, nil
	}
	return nil, err
}
