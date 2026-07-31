package web

import (
	"container/list"
	"sync"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
)

const (
	queryTTL        = 180 * time.Second
	itemTTL         = 300 * time.Second
	maxQueryEntries = 64
	maxItemEntries  = 512
)

type ttlEntry[T any] struct {
	key     any
	value   T
	expires time.Time
}

type ttlCache[T any] struct {
	mu      sync.Mutex
	maxsize int
	ttl     time.Duration
	data    map[any]*list.Element
	order   *list.List // front = oldest
}

func newTTLCache[T any](maxsize int, ttl time.Duration) *ttlCache[T] {
	if maxsize < 1 {
		maxsize = 1
	}
	return &ttlCache[T]{
		maxsize: maxsize,
		ttl:     ttl,
		data:    make(map[any]*list.Element),
		order:   list.New(),
	}
}

func (c *ttlCache[T]) get(key any) (T, bool) {
	var zero T
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.data[key]
	if !ok {
		return zero, false
	}
	ent := el.Value.(*ttlEntry[T])
	if !ent.expires.After(now) {
		c.order.Remove(el)
		delete(c.data, key)
		return zero, false
	}
	c.order.MoveToBack(el)
	return ent.value, true
}

func (c *ttlCache[T]) put(key any, value T) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.data[key]; ok {
		ent := el.Value.(*ttlEntry[T])
		ent.value = value
		ent.expires = now.Add(c.ttl)
		c.order.MoveToBack(el)
		return
	}
	ent := &ttlEntry[T]{key: key, value: value, expires: now.Add(c.ttl)}
	el := c.order.PushBack(ent)
	c.data[key] = el
	for len(c.data) > c.maxsize {
		front := c.order.Front()
		if front == nil {
			break
		}
		old := front.Value.(*ttlEntry[T])
		c.order.Remove(front)
		delete(c.data, old.key)
	}
}

// BrowseCache holds in-process TTL caches for list pages and item details.
type BrowseCache struct {
	queries  *ttlCache[workshop.QueryResult]
	items    *ttlCache[workshop.Item]
	pageNext *ttlCache[string]
}

// NewBrowseCache constructs empty caches.
func NewBrowseCache() *BrowseCache {
	return &BrowseCache{
		queries:  newTTLCache[workshop.QueryResult](maxQueryEntries, queryTTL),
		items:    newTTLCache[workshop.Item](maxItemEntries, itemTTL),
		pageNext: newTTLCache[string](maxQueryEntries*4, queryTTL),
	}
}

// QueryKey builds a cache key for a list query.
func QueryKey(appID int, q, sort string, page, perPage int, cursor string) [6]any {
	return [6]any{appID, q, sort, page, perPage, cursor}
}

func pageChainKey(appID int, q, sort string, page, perPage int) [5]any {
	return [5]any{appID, q, sort, page, perPage}
}

// GetQuery returns a cached query result.
func (c *BrowseCache) GetQuery(key [6]any) (workshop.QueryResult, bool) {
	return c.queries.get(key)
}

// PutQuery stores a query result and seeds item cache + page cursor chain.
func (c *BrowseCache) PutQuery(key [6]any, result workshop.QueryResult) {
	c.queries.put(key, result)
	for _, it := range result.Items {
		c.PutItem(it)
	}
	appID, _ := key[0].(int)
	q, _ := key[1].(string)
	sort, _ := key[2].(string)
	page, _ := key[3].(int)
	perPage, _ := key[4].(int)
	if result.NextCursor != "" {
		c.pageNext.put(pageChainKey(appID, q, sort, page, perPage), result.NextCursor)
	}
}

// CursorForPage resolves the Steam cursor for this page request.
func (c *BrowseCache) CursorForPage(appID int, q, sort string, page, perPage int, cursor string) string {
	if cursor != "" {
		return cursor
	}
	if page <= 1 {
		return ""
	}
	prev, ok := c.pageNext.get(pageChainKey(appID, q, sort, page-1, perPage))
	if !ok {
		return ""
	}
	return prev
}

// GetItem returns a cached item.
func (c *BrowseCache) GetItem(id uint64) (workshop.Item, bool) {
	return c.items.get(int(id))
}

// PutItem stores an item by id.
func (c *BrowseCache) PutItem(item workshop.Item) {
	c.items.put(int(item.ID), item)
}
