// Package workshop implements PublishedFile GetDetails and collection expansion.
package workshop

import (
	"context"
	"fmt"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/steam/cm"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/pb"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
)

const (
	// FileTypeCollection is EWorkshopFileType.Collection.
	FileTypeCollection = 2
	// BatchSize matches Python PUBLISHED_FILE_BATCH.
	BatchSize = 100
	// UMTimeout is the per-call budget (Python UM_TIMEOUT).
	UMTimeout = 30 * time.Second
)

// Item is one published file (download + browse fields).
type Item struct {
	ID               uint64
	Title            string
	ShortDescription string
	PreviewURL       string
	FileType         uint32
	ConsumerAppID    uint32
	HContentFile     uint64 // SteamPipe manifest gid; 0 if not on SteamPipe
	FileSize         uint64
	Subscriptions    uint32
	Favorited        uint32
	Views            uint32
	TimeCreated      uint32
	TimeUpdated      uint32
	Creator          uint64
	Tags             []string
	VoteScore        *float64
	Banned           bool
	Children         []uint64
	Result           uint32
}

// IsCollection is true when file_type == Collection.
func (it Item) IsCollection() bool { return it.FileType == FileTypeCollection }

// GetDetails fetches PublishedFile.GetDetails for the given ids.
// language is an ELanguage value; 0 = Steam default (usually English).
func GetDetails(ctx context.Context, client *cm.Client, ids []uint64, includeChildren bool, language int, log *ui.Log) ([]Item, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []Item
	for start := 0; start < len(ids); start += BatchSize {
		end := start + BatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		items, err := getDetailsBatch(ctx, client, batch, includeChildren, language)
		if err != nil {
			if log != nil {
				log.Err(fmt.Sprintf("Failed to resolve batch %v: %v", batch, err))
			}
			continue
		}
		out = append(out, items...)
	}
	return out, nil
}

func getDetailsBatch(ctx context.Context, client *cm.Client, ids []uint64, includeChildren bool, language int) ([]Item, error) {
	var enc pb.Encoder
	for _, id := range ids {
		enc.Fixed64Field(1, id) // publishedfileids repeated fixed64
	}
	enc.BoolField(2, false)           // includetags
	enc.BoolField(3, false)           // includeadditionalpreviews
	enc.BoolField(4, includeChildren) // includechildren
	enc.BoolField(5, false)           // includekvtags
	enc.BoolField(6, false)           // includevotes
	enc.BoolField(8, true)            // short_description
	enc.BoolField(10, false)          // includeforsaledata
	enc.BoolField(11, false)          // includemetadata
	enc.VarintField(12, uint64(language)) // language

	_, body, err := client.SendUM(ctx, "PublishedFile.GetDetails#1", enc.Bytes(), UMTimeout)
	if err != nil {
		return nil, err
	}
	return parseGetDetailsResponse(body)
}

func parseGetDetailsResponse(body []byte) ([]Item, error) {
	// Share the rich parser used by QueryFiles / GetItemDetails so download
	// and browse paths stay in sync on field numbers.
	return parseGetDetailsResponseRich(body)
}

func parseChildID(data []byte) uint64 {
	fields, err := pb.Decode(data)
	if err != nil {
		return 0
	}
	for _, f := range fields {
		if f.Number == 1 {
			if f.Wire == pb.WireFixed64 {
				return f.Fixed64
			}
			return f.Varint
		}
	}
	return 0
}

// ResolveIDs expands collections recursively and returns downloadable item IDs
// (order-preserving, de-duplicated).
func ResolveIDs(ctx context.Context, client *cm.Client, ids []uint64, log *ui.Log) ([]uint64, error) {
	// de-dupe input preserving order
	seenIn := make(map[uint64]struct{})
	var unique []uint64
	for _, id := range ids {
		if _, ok := seenIn[id]; ok {
			continue
		}
		seenIn[id] = struct{}{}
		unique = append(unique, id)
	}

	var resolved []uint64
	seenOut := make(map[uint64]struct{})

	var walk func([]uint64) error
	walk = func(batch []uint64) error {
		items, err := GetDetails(ctx, client, batch, true, LanguageAuto, log)
		if err != nil {
			return err
		}
		for _, it := range items {
			if it.Result != 0 && it.Result != cm.EResultOK {
				if log != nil {
					log.Warn(fmt.Sprintf("failed to get details for %d (result=%d), skipping", it.ID, it.Result))
				}
				continue
			}
			title := it.Title
			if title == "" {
				title = fmt.Sprintf("%d", it.ID)
			}
			if it.FileType == FileTypeCollection {
				if log != nil {
					log.Info(fmt.Sprintf("  '%s' is a collection, %d items", title, len(it.Children)))
				}
				if len(it.Children) > 0 {
					if err := walk(it.Children); err != nil {
						return err
					}
				}
				continue
			}
			if _, ok := seenOut[it.ID]; ok {
				continue
			}
			seenOut[it.ID] = struct{}{}
			resolved = append(resolved, it.ID)
		}
		return nil
	}

	if err := walk(unique); err != nil {
		return resolved, err
	}
	return resolved, nil
}
