// Package steam is the high-level façade: anonymous session over CM.
package steam

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/proxy"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cm"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
)

// AnonLoginAttempts matches the Python session retry budget.
const AnonLoginAttempts = 5

// Session is a logged-on anonymous Steam CM session.
type Session struct {
	CM *cm.Client
}

// InitSession wires optional proxy, connects to a CM, and performs anonymous login.
func InitSession(ctx context.Context, proxyURL string, log *ui.Log) (*Session, error) {
	var client *cm.Client
	if proxyURL != "" {
		p, err := proxy.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("proxy: %w", err)
		}
		auth := "auth=no"
		if p.HasAuth || p.Username != "" {
			auth = "auth=yes"
		}
		if log != nil {
			log.Dim(fmt.Sprintf("  Proxy wired for CM + CDN + media: %s (%s)", p.RedactedURL(), auth))
		}
		client, err = cm.NewWithProxy(&p)
		if err != nil {
			return nil, err
		}
	} else {
		// No proxy: direct connection with the stealth HTTP transport —
		// TLS omits SNI for store/community edges (SNI-blocked on mainland
		// direct routes) while certificates stay fully verified.
		client = cm.NewDirect()
		if log != nil {
			log.Dim("  Direct connection (stealth TLS: no SNI for blocked hosts, cert verified)")
		}
	}
	if log != nil {
		client.Logf = func(format string, args ...any) {
			log.Dim("  CM: " + fmt.Sprintf(format, args...))
		}
	}

	if log != nil {
		log.Stage("INIT", "Connecting to Steam...")
	}
	if err := client.ConnectAndLogin(ctx, AnonLoginAttempts); err != nil {
		if log != nil {
			log.Err(fmt.Sprintf("Login failed (%v)", err))
			if proxyURL != "" {
				host := "proxy"
				if p, e := proxy.Parse(proxyURL); e == nil {
					host = p.Host
				}
				log.Dim(
					"  Hint: credentials in --proxy are parsed; this usually means " +
						"the exit (" + host + ") cannot complete Steam CM on TCP 27017 " +
						"(web-only / port-filtered proxies often break here). " +
						"Use a full-cone SOCKS that allows arbitrary TCP, not HTTPS-only.",
				)
			}
		}
		return nil, err
	}
	if log != nil {
		log.OK(fmt.Sprintf("Logged on (%d)", client.SteamID()))
		if client.CellID() == 0 {
			log.Warn(
				"Logon cell_id=0 (no region matched). " +
					"Anonymous depot downloads often fail with HTTP 403 from this path. " +
					"Pass --proxy with an exit outside mainland China (e.g. HK/JP/SG/US).",
			)
		}
	}
	return &Session{CM: client}, nil
}

// Close disconnects the CM.
func (s *Session) Close() {
	if s != nil && s.CM != nil {
		s.CM.Disconnect()
	}
}

// HTTP returns the session's HTTP client (honours --proxy when set).
func (s *Session) HTTP() *http.Client {
	if s == nil || s.CM == nil {
		return http.DefaultClient
	}
	if s.CM.HTTP != nil {
		return s.CM.HTTP
	}
	return http.DefaultClient
}

// DefaultInitTimeout is used when the caller passes a context without deadline.
func DefaultInitTimeout() time.Duration { return 90 * time.Second }
