package capability

import (
	"math"
	"net/http"
	"strconv"
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
	missing := &ExecutionError{Code: "missing_output_field", Message: "upstream response did not contain a required field", HTTPStatus: http.StatusBadGateway}
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
	cursor, hasMore := get(raw, "$.data.cursor"), get(raw, "$.data.has_more")
	if !exists || cursor == nil || hasMore == nil {
		return nil, false, missing
	}
	if !douyinInteger(cursor) || !douyinHasMoreValue(hasMore) {
		return nil, false, invalid
	}
	entries, ok := list.([]any)
	if !ok && list != nil {
		return nil, false, invalid
	}
	searchID := text(get(raw, "$.data.search_id"))
	if searchID == "" {
		searchID = text(get(raw, "$.data.log_pb.impr_id"))
	}
	backtrace := text(get(raw, "$.data.backtrace"))
	if backtrace == "" {
		// Some search metadata may be grouped under data.extra. Do not use
		// params.backtrace, which would echo the current request's token.
		backtrace = text(get(raw, "$.data.extra.backtrace"))
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		aweme := get(entry, "$.data.aweme_info")
		if aweme == nil {
			continue
		}
		id := text(get(aweme, "$.aweme_id"))
		if id == "" {
			return nil, false, missing
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

func douyinHasMoreValue(value any) bool {
	switch v := value.(type) {
	case bool:
		return true
	case float64:
		return v == 0 || v == 1
	default:
		return false
	}
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
	playback := func(root any) string {
		url, _ := Lookup(root, "$.play_addr.url_list.0")
		return text(url)
	}
	variants, _ := Lookup(video, "$.bit_rate")
	versions, _ := variants.([]any)
	var bestResolution, bestBitrate float64
	for _, version := range versions {
		url := playback(version)
		if url == "" {
			continue
		}
		w, h := douyinDimensions(version, "$.play_addr")
		if w == 0 {
			w, h = douyinDimensions(version, "$")
		}
		// The short edge is the resolution class (e.g. the 1080 in 1080p),
		// independent of orientation. gear_name is a fallback for missing dimensions.
		resolution := min(w, h)
		if resolution == 0 {
			gear, _ := Lookup(version, "$.gear_name")
			prefix, _, _ := strings.Cut(text(gear), "_")
			pixels, _ := strconv.Atoi(prefix)
			resolution = float64(pixels)
		}
		rate, _ := Lookup(version, "$.bit_rate")
		bitrate, _ := NumberValue(rate)
		if playURL == "" || resolution > bestResolution || (resolution == bestResolution && bitrate > bestBitrate) {
			playURL, width, height = url, w, h
			bestResolution, bestBitrate = resolution, bitrate
		}
	}
	if playURL == "" {
		playURL = playback(video)
		width, height = douyinDimensions(video, "$.play_addr")
	}
	if width == 0 {
		width, height = douyinDimensions(video, "$")
	}
	return
}
