package cdn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
)

// Server is a Steam content server (SteamPipe CS).
type Server struct {
	Type         string
	Host         string
	VHost        string
	HTTPS        bool
	Port         int
	CellID       int
	Load         float64
	WeightedLoad float64
}

// BaseURL returns scheme://host:port for CDN requests.
func (s Server) BaseURL() string {
	scheme := "http"
	port := s.Port
	if s.HTTPS {
		scheme = "https"
		if port == 0 {
			port = 443
		}
	} else if port == 0 {
		port = 80
	}
	host := s.Host
	if host == "" {
		host = s.VHost
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// ServerList is a rotating list of content servers.
type ServerList struct {
	mu      sync.Mutex
	servers []Server
	idx     int
}

// Len returns the number of servers.
func (l *ServerList) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.servers)
}

// Clear empties the list.
func (l *ServerList) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.servers = nil
	l.idx = 0
}

// Set replaces the server list.
func (l *ServerList) Set(ss []Server) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.servers = append([]Server(nil), ss...)
	l.idx = 0
}

// Next returns the current server; rotate advances.
func (l *ServerList) Next(rotate bool) (Server, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.servers) == 0 {
		return Server{}, false
	}
	if rotate {
		l.idx = (l.idx + 1) % len(l.servers)
	}
	return l.servers[l.idx%len(l.servers)], true
}

// FetchFromWebAPI loads CS servers via IContentServerDirectoryService/GetServersForSteamPipe.
func (l *ServerList) FetchFromWebAPI(ctx context.Context, httpClient *http.Client, cellID uint32, maxServers int) error {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if maxServers <= 0 {
		maxServers = 20
	}
	u, _ := url.Parse("https://api.steampowered.com/IContentServerDirectoryService/GetServersForSteamPipe/v1/")
	q := u.Query()
	q.Set("cell_id", strconv.FormatUint(uint64(cellID), 10))
	q.Set("max_servers", strconv.Itoa(maxServers))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GetServersForSteamPipe HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Response struct {
			Servers []struct {
				Type         string  `json:"type"`
				HTTPSSupport string  `json:"https_support"`
				Host         string  `json:"host"`
				VHost        string  `json:"vhost"`
				CellID       int     `json:"cell_id"`
				Load         float64 `json:"load"`
				WeightedLoad float64 `json:"weighted_load"`
			} `json:"servers"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	var ss []Server
	for _, e := range payload.Response.Servers {
		if e.Type == "OpenCache" {
			continue
		}
		https := e.HTTPSSupport == "mandatory"
		port := 80
		if https {
			port = 443
		}
		ss = append(ss, Server{
			Type:         e.Type,
			Host:         e.Host,
			VHost:        e.VHost,
			HTTPS:        https,
			Port:         port,
			CellID:       e.CellID,
			Load:         e.Load,
			WeightedLoad: e.WeightedLoad,
		})
	}
	if len(ss) == 0 {
		return fmt.Errorf("no content servers")
	}
	l.Set(ss)
	return nil
}
