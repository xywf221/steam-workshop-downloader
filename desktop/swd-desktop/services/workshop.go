package services

import (
	"context"
	"fmt"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/steam/workshop"
	"github.com/xywf221/steam-workshop-downloader/internal/util"
)

// WorkshopItem is the typed view of one workshop item for the frontend.
type WorkshopItem struct {
	ID               uint64   `json:"id"`
	Title            string   `json:"title"`
	ShortDescription string   `json:"short_description"`
	PreviewURL       string   `json:"preview_url"`
	FileSize         uint64   `json:"file_size"`
	FileSizeHuman    string   `json:"file_size_human"`
	Subscriptions    uint32   `json:"subscriptions"`
	Favorited        uint32   `json:"favorited"`
	Views            uint32   `json:"views"`
	TimeCreated      uint32   `json:"time_created"`
	TimeUpdated      uint32   `json:"time_updated"`
	IsCollection     bool     `json:"is_collection"`
	ConsumerAppID    uint32   `json:"consumer_appid"`
	Tags             []string `json:"tags"`
	VoteScore        *float64 `json:"vote_score"`
	Banned           bool     `json:"banned"`
	SteamURL         string   `json:"steam_url"`
}

// QueryResult is one page of browse/search results.
type QueryResult struct {
	Total      int            `json:"total"`
	Page       int            `json:"page"`
	PerPage    int            `json:"per_page"`
	TotalPages int            `json:"total_pages"`
	NextCursor string         `json:"next_cursor"`
	Items      []WorkshopItem `json:"items"`
}

// WorkshopService serves browse / search / detail for the Workshop UI.
type WorkshopService struct {
	sess *SessionManager
}

// NewWorkshopService binds the shared session manager.
func NewWorkshopService(sess *SessionManager) *WorkshopService {
	return &WorkshopService{sess: sess}
}

func toItem(it workshop.Item) WorkshopItem {
	sizeHuman := ""
	if it.FileSize > 0 {
		sizeHuman = util.FormatSize(int64(it.FileSize))
	}
	tags := it.Tags
	if tags == nil {
		tags = []string{}
	}
	return WorkshopItem{
		ID:               it.ID,
		Title:            it.Title,
		ShortDescription: it.ShortDescription,
		PreviewURL:       it.PreviewURL,
		FileSize:         it.FileSize,
		FileSizeHuman:    sizeHuman,
		Subscriptions:    it.Subscriptions,
		Favorited:        it.Favorited,
		Views:            it.Views,
		TimeCreated:      it.TimeCreated,
		TimeUpdated:      it.TimeUpdated,
		IsCollection:     it.IsCollection(),
		ConsumerAppID:    it.ConsumerAppID,
		Tags:             tags,
		VoteScore:        it.VoteScore,
		Banned:           it.Banned,
		SteamURL:         fmt.Sprintf("https://steamcommunity.com/sharedfiles/filedetails/?id=%d", it.ID),
	}
}

// Query browses / searches Workshop items.
// sort: trend|votes|subscriptions|new|search (empty → trend or search).
func (s *WorkshopService) Query(appID int, searchText, sort string, page, perPage int, cursor string) (*QueryResult, error) {
	if appID <= 0 {
		return nil, fmt.Errorf("appid must be positive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sess, err := s.sess.CM(ctx)
	if err != nil {
		return nil, fmt.Errorf("steam session: %w", err)
	}
	qt := workshop.ResolveQueryType(sort, searchText)
	res, err := workshop.QueryFiles(ctx, sess.CM, appID, searchText, page, perPage, qt, cursor, nil)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	items := make([]WorkshopItem, 0, len(res.Items))
	for _, it := range res.Items {
		items = append(items, toItem(it))
	}
	totalPages := 1
	if res.Total > 0 && perPage > 0 {
		totalPages = (res.Total + perPage - 1) / perPage
	}
	return &QueryResult{
		Total:      res.Total,
		Page:       res.Page,
		PerPage:    res.PerPage,
		TotalPages: totalPages,
		NextCursor: res.NextCursor,
		Items:      items,
	}, nil
}

// GetItem fetches one item's details.
func (s *WorkshopService) GetItem(workshopID uint64) (*WorkshopItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sess, err := s.sess.CM(ctx)
	if err != nil {
		return nil, fmt.Errorf("steam session: %w", err)
	}
	items, err := workshop.GetDetails(ctx, sess.CM, []uint64{workshopID}, true, nil)
	if err != nil {
		return nil, fmt.Errorf("details: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("item %d not found", workshopID)
	}
	it := toItem(items[0])
	return &it, nil
}
