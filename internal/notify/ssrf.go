package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Webhook URLs are typed in by an admin and then fetched by the server, so a
// careless or compromised admin account could point them at things only the
// server can reach: cloud metadata services, a database admin page, the
// LAN. By default the dispatcher refuses private, loopback and link-local
// destinations, and checks again at connect time so a hostname can't pass
// validation and then resolve to 10.0.0.1 (DNS rebinding). Self-hosters whose
// chat server lives on the LAN can opt in with AllowPrivate.

// ErrPrivateAddress is returned for destinations on private networks.
var ErrPrivateAddress = errors.New("webhook destination is a private or local address")

// CheckURL validates u as a webhook destination: http(s), a host, a sane
// length, and, unless allowPrivate, not a private or local address.
func CheckURL(u string, allowPrivate bool) error {
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" || len(u) > 2000 {
		return errors.New("not a valid http(s) URL")
	}
	if allowPrivate {
		return nil
	}
	host := p.Hostname()
	if host == "localhost" || host == "" {
		return ErrPrivateAddress
	}
	if ip := net.ParseIP(host); ip != nil {
		if isPrivate(ip) {
			return ErrPrivateAddress
		}
		return nil
	}
	// Resolve now for an early, friendly error. The dialer checks again.
	addrs, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil {
		return fmt.Errorf("can't resolve %s", host)
	}
	for _, a := range addrs {
		if isPrivate(a.IP) {
			return ErrPrivateAddress
		}
	}
	return nil
}

func isPrivate(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) ||
		// Carrier-grade NAT and IPv4-mapped forms of the above.
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64)
}

// newClient builds the HTTP client webhooks are delivered with. It never
// follows redirects (a public URL could redirect to a private one) and, unless
// allowPrivate, refuses to connect to private addresses whatever the name
// resolved to.
func newClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var firstErr error
			for _, a := range addrs {
				if !allowPrivate && isPrivate(a.IP) {
					if firstErr == nil {
						firstErr = ErrPrivateAddress
					}
					continue
				}
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
				if err == nil {
					return c, nil
				}
				if firstErr == nil {
					firstErr = err
				}
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("no addresses for %s", host)
			}
			return nil, firstErr
		},
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("webhook redirected; redirects are not followed")
		},
	}
}
