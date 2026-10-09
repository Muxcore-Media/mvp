package main

import (
	"os"
	"path/filepath"
	"strings"
)

// A sidecar subtitle sits beside its video in a flat folder, so the only link
// between them is the file name. The unrestricted listing is lenient about it
// (base + "." or base + " " prefix) because it only offers tracks. A restricted
// listing also grants them, so it needs proof that the item owns the sidecar:
// the sidecar stem is the video stem plus nothing but language/flag tags, and
// no other video in the folder claims it at least as specifically.

// sidecarVideoExts are the container extensions that count as a competing owner
// of a sidecar in the same folder.
var sidecarVideoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".wmv": true,
	".ts": true, ".m2ts": true, ".mts": true, ".mpg": true, ".mpeg": true, ".webm": true,
	".flv": true, ".ogv": true, ".iso": true, ".vob": true, ".divx": true,
}

// sidecarFlagTags are the non-language tags a sidecar suffix may carry.
var sidecarFlagTags = map[string]bool{
	"forced": true, "sdh": true, "hi": true, "cc": true, "hearing": true, "default": true, "full": true,
}

// sidecarLangTags are ISO 639-1 codes and the common ISO 639-2 codes. A
// suffix token that is not here is not a language, so "Alien.Resurrection.en"
// does not parse as "Alien" + language tags.
var sidecarLangTags = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range strings.Fields(`aa ab ae af ak am an ar as av ay az ba be bg bh bi bm bn bo br bs ca ce ch co cr cs cu cv cy da de dv dz ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu gv ha he hi ho hr ht hu hy hz ia id ie ig ii ik io is it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv kw ky la lb lg li ln lo lt lu lv mg mh mi mk ml mn mr ms mt my na nb nd ne ng nl nn no nr nv ny oc oj om or os pa pi pl ps pt qu rm rn ro ru rw sa sc sd se sg si sk sl sm sn so sq sr ss st su sv sw ta te tg th ti tk tl tn to tr ts tt tw ty ug uk ur uz ve vi vo wa wo xh yi yo za zh zu
eng fre fra ger deu spa ita por rus jpn chi zho kor ara hin dut nld swe nor dan fin pol tur heb gre ell cze ces hun rum ron ukr vie tha ind bul hrv srp slv slk slo cat lit lav est ice isl per fas ben tam tel may msa und mul zxx`) {
		m[c] = true
	}
	return m
}()

// sidecarTagToken reports whether one dot-separated suffix token is a language
// (optionally with a region/script such as pt-BR or zh_Hans) or a flag.
func sidecarTagToken(tok string) bool {
	tok = strings.ToLower(tok)
	if sidecarFlagTags[tok] {
		return true
	}
	lang := tok
	if i := strings.IndexAny(tok, "-_"); i >= 0 {
		lang = tok[:i]
		region := tok[i+1:]
		if n := len(region); n < 2 || n > 4 || strings.ContainsAny(region, "-_.") {
			return false
		}
	}
	return sidecarLangTags[lang]
}

// sidecarExtraTags returns the suffix of sidecarStem after videoStem as tag
// tokens, or ok=false when sidecarStem is not videoStem plus tags only.
func sidecarExtraTags(sidecarStem, videoStem string, fold bool) (n int, ok bool) {
	if fold {
		sidecarStem, videoStem = strings.ToLower(sidecarStem), strings.ToLower(videoStem)
	}
	if videoStem == "" || !strings.HasPrefix(sidecarStem, videoStem) {
		return 0, false
	}
	rest := sidecarStem[len(videoStem):]
	if rest == "" {
		return 0, true
	}
	if rest[0] != '.' {
		return 0, false
	}
	toks := strings.Split(rest[1:], ".")
	for _, t := range toks {
		if t == "" || !sidecarTagToken(t) {
			return 0, false
		}
	}
	return len(toks), true
}

// sidecarOwnedBy reports whether videoPath is the one owner of the sidecar
// named sidecarName. The match is conservative: ambiguous or unprovable
// ownership is not ownership.
//
//   - The sidecar stem is the video stem followed only by tag tokens.
//   - No other video in the folder is an equal or more specific owner: another
//     video whose stem also fits and is at least as long (or the same stem under
//     another container) claims it, so neither is granted.
//
// Names compare case-sensitively for the owner and case-insensitively for the
// competitors, so a folder that differs only by case is refused.
func sidecarOwnedBy(sidecarName, videoPath string, entries []os.DirEntry) bool {
	sidecarStem := strings.TrimSuffix(sidecarName, filepath.Ext(sidecarName))
	videoName := filepath.Base(videoPath)
	videoStem := strings.TrimSuffix(videoName, filepath.Ext(videoName))
	if _, ok := sidecarExtraTags(sidecarStem, videoStem, false); !ok {
		return false
	}
	for _, ent := range entries {
		if ent.IsDir() || ent.Name() == videoName {
			continue
		}
		ext := strings.ToLower(filepath.Ext(ent.Name()))
		if !sidecarVideoExts[ext] {
			continue
		}
		otherStem := strings.TrimSuffix(ent.Name(), filepath.Ext(ent.Name()))
		if _, ok := sidecarExtraTags(sidecarStem, otherStem, true); !ok {
			continue
		}
		// The other video fits too. Only a strictly shorter stem leaves this
		// video the more specific owner.
		if len(otherStem) >= len(videoStem) {
			return false
		}
	}
	return true
}
