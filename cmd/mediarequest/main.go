package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	base := flag.String("base", "http://127.0.0.1:5173", "media-ui base URL (proxies to request-media)")
	title := flag.String("title", "Fight Club", "movie title")
	tmdb := flag.Int("tmdb", 550, "TMDB id")
	year := flag.Int("year", 1999, "year")
	requireSearch := flag.Bool("require-search", false, "fail if search returns no results (use with TMDB_FIXTURE=1 or real key)")
	cookieJarPath := flag.String("cookie-jar", "", "Netscape cookie jar from curl -c (optional; for auth'd media-ui)")
	flag.Parse()
	if os.Getenv("SMOKE_REQUIRE_TMDB_SEARCH") == "1" {
		*requireSearch = true
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cookie jar: %v\n", err)
		os.Exit(1)
	}
	if *cookieJarPath != "" {
		if err := loadNetscapeJar(jar, *cookieJarPath); err != nil {
			fmt.Fprintf(os.Stderr, "load cookie jar: %v\n", err)
			os.Exit(1)
		}
	}
	client := &http.Client{Timeout: 20 * time.Second, Jar: jar}

	// Search: soft when TMDB key missing (empty results / error JSON); hard when -require-search.
	searchURL := fmt.Sprintf("%s/api/search?q=%s", strings.TrimRight(*base, "/"), strings.ReplaceAll(*title, " ", "+"))
	resp, err := client.Get(searchURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "search: %v\n", err)
		os.Exit(1)
	}
	searchBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "search HTTP %d: %s\n", resp.StatusCode, searchBody)
		os.Exit(1)
	}
	var search struct {
		Results []struct {
			ID    int32  `json:"id"`
			Title string `json:"title"`
		} `json:"results"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(searchBody, &search)
	fmt.Printf("search results=%d error=%q\n", len(search.Results), search.Error)
	if *requireSearch {
		if search.Error != "" {
			fmt.Fprintf(os.Stderr, "search error (require-search): %s\n", search.Error)
			os.Exit(1)
		}
		if len(search.Results) == 0 {
			fmt.Fprintf(os.Stderr, "search returned 0 results (require-search); body=%s\n", searchBody)
			os.Exit(1)
		}
		found := false
		for _, r := range search.Results {
			if r.ID == int32(*tmdb) || strings.EqualFold(r.Title, *title) {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "search missing %s / tmdb=%d: %s\n", *title, *tmdb, searchBody)
			os.Exit(1)
		}
		fmt.Printf("OK search hit title=%q tmdb=%d\n", search.Results[0].Title, search.Results[0].ID)
	}

	payload, _ := json.Marshal(map[string]any{
		"tmdbId":   *tmdb,
		"title":    *title,
		"year":     *year,
		"overview": "MVP smoke request",
		"poster":   "",
	})
	reqURL := strings.TrimRight(*base, "/") + "/api/request"
	req, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		fmt.Fprintf(os.Stderr, "request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST /api/request: %v\n", err)
		os.Exit(1)
	}
	reqBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "POST /api/request HTTP %d: %s\n", resp.StatusCode, reqBody)
		os.Exit(1)
	}
	var created struct {
		RequestID string `json:"requestId"`
		MovieID   string `json:"movieId"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(reqBody, &created); err != nil {
		fmt.Fprintf(os.Stderr, "decode request: %v\n%s\n", err, reqBody)
		os.Exit(1)
	}
	if created.RequestID == "" {
		fmt.Fprintf(os.Stderr, "empty requestId: %s\n", reqBody)
		os.Exit(1)
	}
	fmt.Printf("requested id=%s movieId=%s status=%s\n", created.RequestID, created.MovieID, created.Status)

	listURL := strings.TrimRight(*base, "/") + "/api/requests"
	resp, err = client.Get(listURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET /api/requests: %v\n", err)
		os.Exit(1)
	}
	listBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "GET /api/requests HTTP %d: %s\n", resp.StatusCode, listBody)
		os.Exit(1)
	}
	if !bytes.Contains(listBody, []byte(created.RequestID)) && !bytes.Contains(listBody, []byte(*title)) {
		fmt.Fprintf(os.Stderr, "requests list missing fixture: %s\n", listBody)
		os.Exit(1)
	}
	fmt.Printf("OK request-media via media-ui (%s)\n", created.Status)
}

func loadNetscapeJar(jar http.CookieJar, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	byHost := map[string][]*http.Cookie{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Netscape comments; keep #HttpOnly_<domain> cookie rows.
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		domain := parts[0]
		name := parts[5]
		value := parts[6]
		host := strings.TrimPrefix(domain, "#HttpOnly_")
		host = strings.TrimPrefix(host, ".")
		// cookiejar ignores Domain for IP hosts; leave Domain empty and key by URL host.
		c := &http.Cookie{Name: name, Value: value, Path: parts[2]}
		byHost[host] = append(byHost[host], c)
	}
	for host, cookies := range byHost {
		u, err := url.Parse("http://" + host + "/")
		if err != nil {
			continue
		}
		jar.SetCookies(u, cookies)
	}
	return nil
}
