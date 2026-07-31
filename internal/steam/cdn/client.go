// Package cdn implements SteamPipe content download: servers, depot keys,
// manifests, and chunks.
package cdn

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/steam/cm"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/crypto"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/manifest"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/pb"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/protocol"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
)

// Client downloads depot manifests and chunks for a logged-on CM session.
type Client struct {
	CM     *cm.Client
	HTTP   *http.Client
	Log    *ui.Log
	Servers *ServerList

	mu         sync.Mutex
	depotKeys  map[uint32][]byte
	chunkCache map[string][]byte // depotID/chunkSHA -> plain
	cacheBytes int
}

// Cap in-memory chunk cache so multi-item sessions do not grow without bound.
const maxChunkCacheBytes = 64 << 20 // 64 MiB


// New builds a CDN client. httpClient should share the proxy dialer with CM.
func New(steam *cm.Client, httpClient *http.Client, log *ui.Log) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{
		CM:         steam,
		HTTP:       httpClient,
		Log:        log,
		Servers:    &ServerList{},
		depotKeys:  make(map[uint32][]byte),
		chunkCache: make(map[string][]byte),
	}
}

// EnsureServers fetches content servers if the list is empty.
func (c *Client) EnsureServers(ctx context.Context) error {
	if c.Servers.Len() > 0 {
		return nil
	}
	cell := uint32(0)
	if c.CM != nil {
		cell = c.CM.CellID()
	}
	if err := c.Servers.FetchFromWebAPI(ctx, c.HTTP, cell, 20); err != nil {
		return err
	}
	srv, ok := c.Servers.Next(false)
	if ok && c.Log != nil {
		c.Log.OK(fmt.Sprintf("Server: %s (cell_id=%d)", srv.Host, srv.CellID))
		if srv.CellID == 0 {
			c.Log.Warn(
				"Content server has cell_id=0 (no region matched). " +
					"Anonymous depot downloads from this PoP often fail with HTTP 403.",
			)
		}
	}
	return nil
}

// cdnGET performs a CDN command with server rotation on transport/5xx errors.
func (c *Client) cdnGET(ctx context.Context, path string) ([]byte, error) {
	if err := c.EnsureServers(ctx); err != nil {
		return nil, err
	}
	var last error
	for try := 0; try < 8; try++ {
		srv, ok := c.Servers.Next(try > 0)
		if !ok {
			return nil, fmt.Errorf("no content servers")
		}
		u := srv.BaseURL() + "/" + path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			last = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if rerr != nil {
			last = rerr
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, nil
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, path)
		}
		last = fmt.Errorf("HTTP %d", resp.StatusCode)
		time.Sleep(200 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("cdn GET failed")
	}
	return nil, last
}

// GetDepotKey fetches (and caches) the depot decryption key via CM.
func (c *Client) GetDepotKey(ctx context.Context, appID, depotID uint32) ([]byte, error) {
	c.mu.Lock()
	if k, ok := c.depotKeys[depotID]; ok {
		c.mu.Unlock()
		return k, nil
	}
	c.mu.Unlock()

	var enc pb.Encoder
	enc.VarintField(1, uint64(depotID))
	enc.VarintField(2, uint64(appID))
	mp := &protocol.MsgProto{
		Msg:  protocol.EMsgClientGetDepotDecryptionKey,
		Body: enc.Bytes(),
	}
	resp, err := c.CM.SendJobAndWait(ctx, mp, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("depot key: %w", err)
	}
	fields, err := pb.Decode(resp.Body)
	if err != nil {
		return nil, err
	}
	var eresult uint64 = cm.EResultFail
	var key []byte
	var gotDepot uint32
	for _, f := range fields {
		switch f.Number {
		case 1:
			eresult = f.Varint
		case 2:
			gotDepot = uint32(f.Varint)
		case 3:
			key = f.Bytes
		}
	}
	_ = gotDepot
	if eresult != cm.EResultOK || len(key) == 0 {
		return nil, fmt.Errorf("depot key eresult=%d keylen=%d", eresult, len(key))
	}
	c.mu.Lock()
	c.depotKeys[depotID] = key
	c.mu.Unlock()
	return key, nil
}

// GetManifestRequestCode authenticates a manifest download.
func (c *Client) GetManifestRequestCode(ctx context.Context, appID, depotID uint32, manifestGID uint64) (uint64, error) {
	var enc pb.Encoder
	enc.VarintField(1, uint64(appID))
	enc.VarintField(2, uint64(depotID))
	enc.VarintField(3, manifestGID) // uint64
	_, body, err := c.CM.SendUM(ctx, "ContentServerDirectory.GetManifestRequestCode#1", enc.Bytes(), 30*time.Second)
	if err != nil {
		return 0, err
	}
	fields, err := pb.Decode(body)
	if err != nil {
		return 0, err
	}
	for _, f := range fields {
		if f.Number == 1 {
			if f.Wire == pb.WireFixed64 {
				return f.Fixed64, nil
			}
			return f.Varint, nil
		}
	}
	return 0, fmt.Errorf("no manifest_request_code in response")
}

// GetManifest downloads and parses a depot manifest.
func (c *Client) GetManifest(ctx context.Context, appID, depotID uint32, manifestGID, code uint64) (*manifest.Manifest, error) {
	var path string
	if code != 0 {
		path = fmt.Sprintf("depot/%d/manifest/%d/5/%d", depotID, manifestGID, code)
	} else {
		path = fmt.Sprintf("depot/%d/manifest/%d/5", depotID, manifestGID)
	}
	raw, err := c.cdnGET(ctx, path)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return nil, err
	}
	key, err := c.GetDepotKey(ctx, appID, depotID)
	if err != nil {
		return nil, err
	}
	if err := m.DecryptFilenames(key); err != nil {
		return nil, err
	}
	m.AppID = appID
	m.DepotID = depotID
	return m, nil
}

// ManifestForWorkshopItem resolves a workshop item to its SteamPipe manifest.
func (c *Client) ManifestForWorkshopItem(ctx context.Context, itemID uint64) (*manifest.Manifest, error) {
	items, err := workshop.GetDetails(ctx, c.CM, []uint64{itemID}, false, c.Log)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no details for workshop item %d", itemID)
	}
	it := items[0]
	if it.Result != 0 && it.Result != cm.EResultOK {
		return nil, fmt.Errorf("workshop item %d result=%d", itemID, it.Result)
	}
	if it.HContentFile == 0 {
		return nil, fmt.Errorf("workshop file %d is not on SteamPipe", itemID)
	}
	appID := it.ConsumerAppID
	depotID := it.ConsumerAppID
	code, err := c.GetManifestRequestCode(ctx, appID, depotID, it.HContentFile)
	if err != nil {
		return nil, fmt.Errorf("manifest code: %w", err)
	}
	m, err := c.GetManifest(ctx, appID, depotID, it.HContentFile, code)
	if err != nil {
		return nil, err
	}
	if it.Title != "" {
		m.Name = it.Title
	}
	return m, nil
}

// GetChunk downloads, decrypts, and returns a decompressed chunk.
// chunkID is the hex encoding of the chunk SHA (as used in CDN URLs).
// decompress is injected to avoid an import cycle with internal/dll.
func (c *Client) GetChunk(ctx context.Context, appID, depotID uint32, chunkID string, decompress func([]byte) ([]byte, error)) ([]byte, error) {
	cacheKey := fmt.Sprintf("%d/%s", depotID, chunkID)
	c.mu.Lock()
	if data, ok := c.chunkCache[cacheKey]; ok {
		c.mu.Unlock()
		return data, nil
	}
	c.mu.Unlock()

	// Parallel-safe: HTTP + crypto are re-entrant; DLL decompress has its own mutex.
	raw, err := c.cdnGET(ctx, fmt.Sprintf("depot/%d/chunk/%s", depotID, chunkID))
	if err != nil {
		return nil, err
	}
	key, err := c.GetDepotKey(ctx, appID, depotID)
	if err != nil {
		return nil, err
	}
	plain, err := crypto.SymmetricDecrypt(raw, key)
	if err != nil {
		return nil, fmt.Errorf("chunk decrypt: %w", err)
	}
	if decompress == nil {
		return nil, fmt.Errorf("no decompressor configured")
	}
	data, err := decompress(plain)
	if err != nil {
		return nil, fmt.Errorf("chunk decompress: %w", err)
	}
	c.mu.Lock()
	if _, ok := c.chunkCache[cacheKey]; !ok {
		// Drop arbitrary entries when over budget (map iteration order is fine).
		for c.cacheBytes+len(data) > maxChunkCacheBytes && len(c.chunkCache) > 0 {
			for k, v := range c.chunkCache {
				c.cacheBytes -= len(v)
				delete(c.chunkCache, k)
				break
			}
		}
		if c.cacheBytes+len(data) <= maxChunkCacheBytes {
			c.chunkCache[cacheKey] = data
			c.cacheBytes += len(data)
		}
	}
	c.mu.Unlock()
	return data, nil
}

// ReadFile assembles a full file from its manifest chunks in offset order.
func (c *Client) ReadFile(ctx context.Context, m *manifest.Manifest, f *manifest.File, decompress func([]byte) ([]byte, error)) ([]byte, error) {
	chunks := append([]manifest.Chunk(nil), f.Chunks...)
	manifest.SortChunksByOffset(chunks)
	out := make([]byte, 0, f.Size)
	for _, ch := range chunks {
		id := fmt.Sprintf("%x", ch.SHA)
		data, err := c.GetChunk(ctx, m.AppID, m.DepotID, id, decompress)
		if err != nil {
			return nil, fmt.Errorf("chunk %s: %w", id, err)
		}
		// Guard against oversize decompress
		need := int(ch.OriginalSize)
		if need > 0 && len(data) > need {
			data = data[:need]
		}
		out = append(out, data...)
	}
	if f.Size > 0 && uint64(len(out)) != f.Size {
		// still return; SHA check at higher layer may catch
		if c.Log != nil {
			c.Log.Warn(fmt.Sprintf("file %s size mismatch got=%d want=%d", f.Filename, len(out), f.Size))
		}
	}
	return out, nil
}
