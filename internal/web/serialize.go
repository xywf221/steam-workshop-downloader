package web

import (
	"fmt"

	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
	"github.com/xywf221/steam-workshop-downloader/internal/util"
)

// ItemDict is the JSON / template view of a workshop item.
type ItemDict map[string]any

// ItemToDict serializes a workshop item for API / templates.
func ItemToDict(item workshop.Item, proxyURL string) ItemDict {
	preview := item.PreviewURL
	sizeHuman := ""
	if item.FileSize > 0 {
		sizeHuman = util.FormatSize(int64(item.FileSize))
	}
	var vote any
	if item.VoteScore != nil {
		vote = *item.VoteScore
	}
	tags := item.Tags
	if tags == nil {
		tags = []string{}
	}
	return ItemDict{
		"id":                item.ID,
		"title":             item.Title,
		"short_description": item.ShortDescription,
		"preview_url":       preview,
		"preview_src":       DisplayPreviewURL(preview, proxyURL),
		"file_size":         item.FileSize,
		"file_size_human":   sizeHuman,
		"subscriptions":     item.Subscriptions,
		"favorited":         item.Favorited,
		"views":             item.Views,
		"time_created":      item.TimeCreated,
		"time_updated":      item.TimeUpdated,
		"file_type":         item.FileType,
		"is_collection":     item.IsCollection(),
		"creator":           item.Creator,
		"consumer_appid":    item.ConsumerAppID,
		"tags":              tags,
		"vote_score":        vote,
		"banned":            item.Banned,
		"steam_url":         fmt.Sprintf("https://steamcommunity.com/sharedfiles/filedetails/?id=%d", item.ID),
	}
}

// QueryToDict serializes a QueryResult.
func QueryToDict(result workshop.QueryResult, proxyURL string) map[string]any {
	items := make([]ItemDict, 0, len(result.Items))
	for _, it := range result.Items {
		items = append(items, ItemToDict(it, proxyURL))
	}
	return map[string]any{
		"total":       result.Total,
		"page":        result.Page,
		"per_page":    result.PerPage,
		"next_cursor": result.NextCursor,
		"items":       items,
	}
}
