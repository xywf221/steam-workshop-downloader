package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) indexPage(w http.ResponseWriter, r *http.Request) {
	data := s.baseData()
	appidRaw := r.URL.Query().Get("appid")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	sort := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 {
		perPage = defaultQueryPageSize
	}
	if perPage > maxQueryPageSize {
		perPage = maxQueryPageSize
	}
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))

	data["AppID"] = appidRaw
	data["Q"] = q
	data["Page"] = page
	data["PerPage"] = perPage
	data["Cursor"] = cursor
	data["HasQuery"] = strings.TrimSpace(appidRaw) != ""
	data["ProxyEnabled"] = s.cfg.ProxyURL != ""
	data["Items"] = []ItemDict{}
	data["Total"] = 0
	data["TotalPages"] = 1
	data["NextCursor"] = ""
	data["Error"] = ""

	sortEffective := sort
	if sortEffective == "" {
		if q != "" {
			sortEffective = "search"
		} else {
			sortEffective = "trend"
		}
	}
	data["Sort"] = sortEffective

	if strings.TrimSpace(appidRaw) != "" {
		appID, err := strconv.Atoi(appidRaw)
		if err != nil || appID <= 0 {
			data["Error"] = "AppID must be a positive integer."
		} else {
			result, err := s.runQuery(r.Context(), appID, q, page, perPage, sort, cursor)
			if err != nil {
				if s.log != nil {
					s.log.Err(fmt.Sprintf("QueryFiles failed: %v", err))
				}
				data["Error"] = fmt.Sprintf("Steam query failed: %v", err)
			} else {
				items := make([]ItemDict, 0, len(result.Items))
				for _, it := range result.Items {
					items = append(items, ItemToDict(it, s.cfg.ProxyURL))
				}
				totalPages := 1
				if result.Total > 0 {
					totalPages = (result.Total + perPage - 1) / perPage
					if totalPages < 1 {
						totalPages = 1
					}
				}
				data["Items"] = items
				data["Total"] = result.Total
				data["TotalPages"] = totalPages
				data["NextCursor"] = result.NextCursor
				data["Page"] = result.Page
			}
		}
	}
	s.render(w, "index.html", data)
}

func (s *Server) itemPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		http.Error(w, "invalid item id", 400)
		return
	}
	data := s.baseData()
	data["ItemID"] = id
	data["AppID"] = r.URL.Query().Get("appid")
	data["ProxyEnabled"] = s.cfg.ProxyURL != ""
	data["Error"] = ""
	data["Item"] = nil

	item, err := s.runDetail(r.Context(), id)
	if err != nil {
		if s.log != nil {
			s.log.Err(fmt.Sprintf("GetDetails failed for %d: %v", id, err))
		}
		data["Error"] = fmt.Sprintf("Steam GetDetails failed: %v", err)
	} else if item == nil {
		data["Error"] = fmt.Sprintf("Workshop item %d not found.", id)
	} else {
		data["Item"] = ItemToDict(*item, s.cfg.ProxyURL)
	}
	s.render(w, "detail.html", data)
}

func (s *Server) downloadsPage(w http.ResponseWriter, r *http.Request) {
	data := s.baseData()
	jobs := s.dlMgr.ListJobs(100)
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.ToDict())
	}
	data["Jobs"] = out
	data["Counts"] = s.dlMgr.Counts()
	s.render(w, "downloads.html", data)
}
