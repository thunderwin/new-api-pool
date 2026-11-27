package capability

import (
	"math"
	"net/http"
	"strings"
	"unicode/utf8"
)

func validateDouyinInput(input map[string]any) *ExecutionError {
	invalid := func(message string) *ExecutionError {
		return &ExecutionError{Code: "invalid_request", Message: message, HTTPStatus: http.StatusBadRequest}
	}
	keyword, ok := input["keyword"].(string)
	if !ok || strings.TrimSpace(keyword) == "" || utf8.RuneCountInString(keyword) > 50 {
		return invalid("keyword must be a string of 1 to 50 characters")
	}
	if cursor, exists := input["cursor"]; exists && !douyinInteger(cursor) {
		return invalid("cursor must be a non-negative integer")
	}
	for _, field := range []string{"search_id", "backtrace"} {
		if value, exists := input[field]; exists {
			if s, ok := value.(string); !ok || len(s) > 4096 {
				return invalid(field + " must be a string of at most 4096 bytes")
			}
		}
	}
	return nil
}

func douyinInteger(value any) bool {
	if _, isString := value.(string); isString {
		return false
	}
	n, ok := NumberValue(value)
	return ok && n >= 0 && n <= (1<<53)-1 && math.Trunc(n) == n
}

func projectDouyinSearch(raw map[string]any, secret string) (map[string]any, bool, *ExecutionError) {
	invalid := &ExecutionError{Code: "invalid_output_field", Message: "upstream response field has an invalid format", HTTPStatus: http.StatusBadGateway}
	get := func(root any, path string) any {
		value, _ := Lookup(root, path)
		return value
	}
	text := func(value any) string {
		s, _ := value.(string)
		if secret != "" && strings.Contains(s, secret) {
			return ""
		}
		return s
	}
	list, exists := Lookup(raw, "$.data.business_data")
	if !exists {
		return nil, false, &ExecutionError{Code: "missing_output_field", Message: "upstream response did not contain the search results", HTTPStatus: http.StatusBadGateway}
	}
	entries, ok := list.([]any)
	if !ok && list != nil {
		return nil, false, invalid
	}
	config := get(raw, "$.data.business_config")
	cursor := get(config, "$.next_page.cursor")
	if !douyinInteger(cursor) {
		cursor = nil
	}
	hasMore := 0
	if more := get(config, "$.has_more"); douyinInteger(more) {
		if n, _ := NumberValue(more); n == 1 {
			hasMore = 1
		}
	}
	searchID := text(get(config, "$.next_page.search_id"))
	backtrace := text(get(config, "$.backtrace"))
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		aweme := get(entry, "$.data.aweme_info")
		if aweme == nil {
			continue
		}
		id := text(get(aweme, "$.aweme_id"))
		if id == "" {
			return nil, false, invalid
		}
		desc := []rune(text(get(aweme, "$.desc")))
		if len(desc) > 120 {
			desc = desc[:120]
		}
		video := get(aweme, "$.video")
		var seconds, likes any
		if duration, ok := NumberValue(get(video, "$.duration")); ok && duration >= 0 && !math.IsInf(duration, 0) {
			seconds = duration / 1000 // TikHub documents video.duration in milliseconds.
		}
		if n := get(aweme, "$.statistics.digg_count"); douyinInteger(n) {
			likes = n
		}
		playURL, width, height := douyinPlayback(video, text)
		items = append(items, map[string]any{
			"id": id, "desc": string(desc), "author": text(get(aweme, "$.author.nickname")),
			"duration_s": seconds, "likes": likes, "share_url": text(get(aweme, "$.share_url")),
			"play_url": playURL, "width": width, "height": height,
		})
	}
	return map[string]any{"items": items, "cursor": cursor, "search_id": searchID, "backtrace": backtrace, "has_more": hasMore}, len(items) == 0, nil
}

func douyinDimensions(root any, base string) (float64, float64) {
	w, _ := Lookup(root, base+".width")
	h, _ := Lookup(root, base+".height")
	width, _ := NumberValue(w)
	height, _ := NumberValue(h)
	if !(width > 0 && height > 0) || math.IsInf(width, 0) || math.IsInf(height, 0) {
		return 0, 0
	}
	return width, height
}

func douyinPlayback(video any, text func(any) string) (playURL string, width, height float64) {
	var bestResolution, bestBitrate float64
	consider := func(candidate any) {
		value, _ := Lookup(candidate, "$.play_addr.url_list.0")
		url := text(value)
		if url == "" {
			return
		}
		w, h := douyinDimensions(candidate, "$.play_addr")
		if w == 0 {
			w, h = douyinDimensions(candidate, "$")
		}
		resolution := min(w, h) // Short edge, independent of orientation.
		rate, _ := Lookup(candidate, "$.bit_rate")
		bitrate, _ := NumberValue(rate)
		if playURL == "" || resolution > bestResolution || (resolution == bestResolution && bitrate > bestBitrate) {
			playURL, width, height = url, w, h
			bestResolution, bestBitrate = resolution, bitrate
		}
	}
	consider(video)
	variants, _ := Lookup(video, "$.bit_rate")
	versions, _ := variants.([]any)
	for _, version := range versions {
		consider(version)
	}
	return
}
