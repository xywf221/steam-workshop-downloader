package web

import (
	"testing"

	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
)

func TestBrowseCacheCursorChain(t *testing.T) {
	c := NewBrowseCache()
	key := QueryKey(294100, "", "trend", 1, 20, "*", 0)
	c.PutQuery(key, workshop.QueryResult{
		Total:      100,
		Page:       1,
		PerPage:    20,
		NextCursor: "CURSOR2",
		Items: []workshop.Item{
			{ID: 1, Title: "a"},
		},
	})
	if _, ok := c.GetItem(1, 0); !ok {
		t.Fatal("item should be seeded from query")
	}
	got := c.CursorForPage(294100, "", "trend", 2, 20, 0, "")
	if got != "CURSOR2" {
		t.Fatalf("cursor chain: got %q", got)
	}
	if c.CursorForPage(294100, "", "trend", 2, 20, 0, "EXPLICIT") != "EXPLICIT" {
		t.Fatal("explicit cursor should win")
	}
}
