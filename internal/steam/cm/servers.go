package cm

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Server is a Steam CM endpoint.
type Server struct {
	Host string
	Port int
}

func (s Server) Addr() string {
	return net.JoinHostPort(s.Host, fmt.Sprintf("%d", s.Port))
}

// ServerList holds discovered CM addresses with simple bad-marking.
type ServerList struct {
	mu      sync.Mutex
	servers []Server
	bad     map[string]time.Time
	idx     int
}

// NewServerList creates an empty list.
func NewServerList() *ServerList {
	return &ServerList{bad: make(map[string]time.Time)}
}

// Len returns the number of known servers.
func (l *ServerList) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.servers)
}

// Add appends servers (dedup by host:port).
func (l *ServerList) Add(ss ...Server) {
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := make(map[string]struct{}, len(l.servers))
	for _, s := range l.servers {
		seen[s.Addr()] = struct{}{}
	}
	for _, s := range ss {
		if _, ok := seen[s.Addr()]; ok {
			continue
		}
		l.servers = append(l.servers, s)
		seen[s.Addr()] = struct{}{}
	}
}

// MarkBad temporarily deprioritises a server.
func (l *ServerList) MarkBad(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bad[addr] = time.Now()
}

// Next returns the next server to try (skips recently bad ones when possible).
func (l *ServerList) Next() (Server, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.servers) == 0 {
		return Server{}, false
	}
	n := len(l.servers)
	for try := 0; try < n; try++ {
		s := l.servers[l.idx%n]
		l.idx++
		if t, ok := l.bad[s.Addr()]; ok && time.Since(t) < 5*time.Minute {
			continue
		}
		return s, true
	}
	// All bad — return next anyway.
	s := l.servers[l.idx%n]
	l.idx++
	return s, true
}

// BootstrapWebAPI fetches CM list from ISteamDirectory/GetCMList.
// httpClient should already honour any proxy (via custom Transport).
func (l *ServerList) BootstrapWebAPI(ctx context.Context, httpClient *http.Client) error {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.steampowered.com/ISteamDirectory/GetCMList/v1/?cellid=0&format=json", nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GetCMList HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Response struct {
			ServerList []string `json:"serverlist"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	var ss []Server
	for _, entry := range payload.Response.ServerList {
		host, portStr, err := net.SplitHostPort(entry)
		if err != nil {
			continue
		}
		var port int
		fmt.Sscanf(portStr, "%d", &port)
		if port == 0 {
			continue
		}
		ss = append(ss, Server{Host: host, Port: port})
	}
	if len(ss) == 0 {
		return fmt.Errorf("GetCMList returned no servers")
	}
	l.Add(ss...)
	return nil
}

// BootstrapDNS adds the classic cm0.steampowered.com:27017 fallback.
func (l *ServerList) BootstrapDNS() {
	l.Add(Server{Host: "cm0.steampowered.com", Port: 27017})
}
