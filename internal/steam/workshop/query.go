package workshop

import (
	"context"
	"fmt"
	"math"

	"github.com/xywf221/steam-workshop-downloader/internal/steam/cm"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/pb"
	"github.com/xywf221/steam-workshop-downloader/internal/ui"
)

// EPublishedFileQueryType values used by the web UI.
const (
	QueryTypeRankedByVote                    = 0
	QueryTypeRankedByPublicationDate         = 1
	QueryTypeRankedByTrend                   = 3
	QueryTypeRankedByTotalUniqueSubscriptions = 9
	QueryTypeRankedByTextSearch              = 12
)

// ELanguage values for the language field in PublishedFile requests.
const (
	LanguageAuto               = 0
	LanguageEnglish            = 1
	LanguageGerman             = 2
	LanguageFrench             = 3
	LanguageItalian            = 4
	LanguageKorean             = 5
	LanguageSpanish            = 6
	LanguageSimplifiedChinese  = 7
	LanguageTraditionalChinese = 8
	LanguageRussian            = 9
	LanguageJapanese           = 10
	LanguagePortuguese         = 11
	LanguagePolish             = 12
	LanguageDanish             = 13
	LanguageDutch              = 14
	LanguageSwedish            = 15
	LanguageThai               = 16
	LanguageTurkish            = 17
	LanguageArabic             = 18
	LanguageFinnish            = 19
	LanguageGreek              = 20
	LanguageCzech              = 21
	LanguageHungarian          = 22
	LanguageRomanian           = 23
)

// QueryCursorStart is Steam's first-page cursor token for QueryFiles.
const QueryCursorStart = "*"

// SortToQueryType maps UI sort names to EPublishedFileQueryType.
var SortToQueryType = map[string]int{
	"votes":        QueryTypeRankedByVote,
	"new":          QueryTypeRankedByPublicationDate,
	"trend":        QueryTypeRankedByTrend,
	"subscriptions": QueryTypeRankedByTotalUniqueSubscriptions,
	"search":       QueryTypeRankedByTextSearch,
}

// ResolveQueryType maps a UI sort name + optional search text to a query type.
func ResolveQueryType(sort, searchText string) int {
	key := sort
	if key != "" {
		if qt, ok := SortToQueryType[key]; ok {
			return qt
		}
	}
	if searchText != "" {
		return QueryTypeRankedByTextSearch
	}
	return QueryTypeRankedByTrend
}

// QueryResult is one page of PublishedFile.QueryFiles.
type QueryResult struct {
	Total      int
	Items      []Item
	Page       int
	PerPage    int
	NextCursor string
}

// QueryFiles browses / searches Workshop items for appID.
// Prefer cursor pagination: empty cursor on page 1 becomes "*"; pass NextCursor thereafter.
// language is an ELanguage value; 0 = Steam default (usually English).
func QueryFiles(
	ctx context.Context,
	client *cm.Client,
	appID int,
	searchText string,
	page, perPage, queryType int,
	cursor string,
	language int,
	log *ui.Log,
) (QueryResult, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 1
	}
	if queryType < 0 {
		queryType = ResolveQueryType("", searchText)
	}

	useCursor := cursor
	if useCursor == "" {
		if page <= 1 {
			useCursor = QueryCursorStart
		}
	}

	var enc pb.Encoder
	enc.VarintField(1, uint64(queryType)) // query_type
	if useCursor != "" {
		enc.VarintField(2, 1) // page=1 when using cursor
	} else {
		enc.VarintField(2, uint64(page))
	}
	enc.VarintField(3, uint64(perPage)) // numperpage
	enc.VarintField(5, uint64(appID))   // appid
	if searchText != "" {
		enc.StringField(11, searchText) // search_text
	}
	enc.BoolField(17, true)  // return_vote_data
	enc.BoolField(18, true)  // return_tags
	enc.BoolField(20, true)  // return_previews
	enc.BoolField(22, true)  // return_short_description
	enc.VarintField(31, 300)               // cache_max_age_seconds
	enc.VarintField(33, uint64(language)) // language
	enc.BoolField(37, false) // return_details
	enc.BoolField(38, true)  // strip_description_bbcode
	if useCursor != "" {
		enc.StringField(39, useCursor) // cursor
	}

	_, body, err := client.SendUM(ctx, "PublishedFile.QueryFiles#1", enc.Bytes(), UMTimeout)
	if err != nil {
		return QueryResult{}, err
	}
	return parseQueryFilesResponse(body, page, perPage)
}

func parseQueryFilesResponse(body []byte, page, perPage int) (QueryResult, error) {
	fields, err := pb.Decode(body)
	if err != nil {
		return QueryResult{}, err
	}
	qr := QueryResult{Page: page, PerPage: perPage}
	for _, f := range fields {
		switch f.Number {
		case 1: // total
			qr.Total = int(f.Varint)
		case 2: // publishedfiledetails
			if f.Wire == pb.WireBytes {
				it, ok := parseRichDetails(f.Bytes)
				if ok {
					qr.Items = append(qr.Items, it)
				}
			}
		case 3: // next_cursor
			qr.NextCursor = string(f.Bytes)
		}
	}
	return qr, nil
}

// GetItemDetails fetches one item with tags / votes / description for the UI.
// language is an ELanguage value; 0 = Steam default (usually English).
func GetItemDetails(ctx context.Context, client *cm.Client, id uint64, language int, log *ui.Log) (*Item, error) {
	var enc pb.Encoder
	enc.Fixed64Field(1, id)
	enc.BoolField(2, true)  // includetags
	enc.BoolField(3, false) // includeadditionalpreviews
	enc.BoolField(4, false) // includechildren
	enc.BoolField(5, false) // includekvtags
	enc.BoolField(6, true)  // includevotes
	enc.BoolField(8, true)  // short_description
	enc.BoolField(10, false)
	enc.BoolField(11, false)
	enc.VarintField(12, uint64(language)) // language

	_, body, err := client.SendUM(ctx, "PublishedFile.GetDetails#1", enc.Bytes(), UMTimeout)
	if err != nil {
		return nil, err
	}
	items, err := parseGetDetailsResponseRich(body)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	it := items[0]
	return &it, nil
}

func parseGetDetailsResponseRich(body []byte) ([]Item, error) {
	fields, err := pb.Decode(body)
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, f := range fields {
		if f.Number != 1 || f.Wire != pb.WireBytes {
			continue
		}
		it, ok := parseRichDetails(f.Bytes)
		if ok {
			items = append(items, it)
		}
	}
	return items, nil
}

// parseRichDetails maps a PublishedFileDetails message to Item (UI + download fields).
// Returns ok=false when result is non-OK or id missing.
func parseRichDetails(data []byte) (Item, bool) {
	fields, err := pb.Decode(data)
	if err != nil {
		return Item{}, false
	}
	var it Item
	var hasResult bool
	for _, f := range fields {
		switch f.Number {
		case 1: // result
			it.Result = uint32(f.Varint)
			hasResult = true
		case 2: // publishedfileid
			if f.Wire == pb.WireFixed64 {
				it.ID = f.Fixed64
			} else {
				it.ID = f.Varint
			}
		case 3: // creator fixed64
			if f.Wire == pb.WireFixed64 {
				it.Creator = f.Fixed64
			} else {
				it.Creator = f.Varint
			}
		case 5: // consumer_appid
			it.ConsumerAppID = uint32(f.Varint)
		case 8: // file_size
			if f.Wire == pb.WireFixed64 {
				it.FileSize = f.Fixed64
			} else {
				it.FileSize = f.Varint
			}
		case 11: // preview_url
			it.PreviewURL = string(f.Bytes)
		case 14: // hcontent_file
			if f.Wire == pb.WireFixed64 {
				it.HContentFile = f.Fixed64
			} else {
				it.HContentFile = f.Varint
			}
		case 16: // title
			it.Title = string(f.Bytes)
		case 17: // file_description
			if it.ShortDescription == "" {
				it.ShortDescription = string(f.Bytes)
			}
		case 18: // short_description
			it.ShortDescription = string(f.Bytes)
		case 19: // time_created
			it.TimeCreated = uint32(f.Varint)
		case 20: // time_updated
			it.TimeUpdated = uint32(f.Varint)
		case 28: // banned
			it.Banned = f.Varint != 0
		case 34: // file_type
			it.FileType = uint32(f.Varint)
		case 36: // subscriptions
			it.Subscriptions = uint32(f.Varint)
		case 37: // favorited
			it.Favorited = uint32(f.Varint)
		case 39: // lifetime_subscriptions
			if it.Subscriptions == 0 {
				it.Subscriptions = uint32(f.Varint)
			}
		case 40: // lifetime_favorited
			if it.Favorited == 0 {
				it.Favorited = uint32(f.Varint)
			}
		case 42: // views
			it.Views = uint32(f.Varint)
		case 45: // image_url fallback
			if it.PreviewURL == "" {
				it.PreviewURL = string(f.Bytes)
			}
		case 52: // tags
			if f.Wire == pb.WireBytes {
				if tag := parseTagName(f.Bytes); tag != "" {
					it.Tags = append(it.Tags, tag)
				}
			}
		case 53: // children
			if f.Wire == pb.WireBytes {
				if cid := parseChildID(f.Bytes); cid != 0 {
					it.Children = append(it.Children, cid)
				}
			}
		case 55: // vote_data
			if f.Wire == pb.WireBytes {
				if score, ok := parseVoteScore(f.Bytes); ok {
					it.VoteScore = &score
				}
			}
		}
	}
	if it.ID == 0 {
		return Item{}, false
	}
	// QueryFiles often omits result or sets 0; treat missing/0 as OK.
	if hasResult && it.Result != 0 && it.Result != cm.EResultOK {
		return Item{}, false
	}
	if it.Title == "" {
		it.Title = fmt.Sprintf("%d", it.ID)
	}
	return it, true
}

func parseTagName(data []byte) string {
	fields, err := pb.Decode(data)
	if err != nil {
		return ""
	}
	var tag, display string
	for _, f := range fields {
		switch f.Number {
		case 1:
			tag = string(f.Bytes)
		case 3:
			display = string(f.Bytes)
		}
	}
	if tag != "" {
		return tag
	}
	return display
}

func parseVoteScore(data []byte) (float64, bool) {
	fields, err := pb.Decode(data)
	if err != nil {
		return 0, false
	}
	for _, f := range fields {
		if f.Number != 1 {
			continue
		}
		// TYPE_FLOAT = wire fixed32
		if f.Wire == pb.WireFixed32 {
			return float64(math.Float32frombits(f.Fixed32)), true
		}
		if f.Wire == pb.WireFixed64 {
			return math.Float64frombits(f.Fixed64), true
		}
	}
	return 0, false
}
