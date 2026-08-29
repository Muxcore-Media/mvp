package main

import (
	"encoding/json"
	"strings"
)

type parentalPrefs struct {
	MaxParentalRating string `json:"max_parental_rating"`
	BlockedTags       string `json:"blocked_tags"`
	AllowedTags       string `json:"allowed_tags"`
	AllowUnrated      bool   `json:"allow_unrated"`
}

func parentalBlocksPlayback(prefsJSON json.RawMessage, tags, rating string, unrated bool) bool {
	if len(prefsJSON) == 0 {
		return false
	}
	var prefs map[string]json.RawMessage
	if json.Unmarshal(prefsJSON, &prefs) != nil {
		return false
	}
	raw, ok := prefs["parental"]
	if !ok || len(raw) == 0 {
		return false
	}
	var p parentalPrefs
	if json.Unmarshal(raw, &p) != nil {
		return false
	}
	if unrated && !p.AllowUnrated {
		return true
	}
	tagLower := strings.ToLower(tags)
	for _, bt := range strings.Split(strings.ToLower(p.BlockedTags), ",") {
		bt = strings.TrimSpace(bt)
		if bt != "" && strings.Contains(tagLower, bt) {
			return true
		}
	}
	_ = rating // reserved for future rating ladder checks
	return false
}
