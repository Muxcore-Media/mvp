package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	listen := flag.String("listen", envOr("MEDIA_UI_LISTEN", ":5173"), "HTTP listen address")
	dist := flag.String("dist", envOr("MEDIA_UI_DIST", ""), "path to media-ui dist-app (required)")
	moviesGRPC := flag.String("movies-grpc", envOr("MOVIES_GRPC_CLIENT_ADDR", "127.0.0.1:9420"), "media-movies gRPC")
	tvGRPC := flag.String("tv-grpc", envOr("TVSHOWS_GRPC_CLIENT_ADDR", "127.0.0.1:9440"), "media-tvshows gRPC")
	jellyfinGRPC := flag.String("jellyfin-grpc", envOr("JELLYFIN_GRPC_CLIENT_ADDR", "127.0.0.1:9475"), "jellyfin bridge gRPC")
	moviesHTTP := flag.String("movies-http", envOr("MOVIES_HTTP_URL", "http://127.0.0.1:9430"), "media-movies HTTP (images/stream)")
	tvHTTP := flag.String("tv-http", envOr("TVSHOWS_HTTP_URL", "http://127.0.0.1:9450"), "media-tvshows HTTP (images)")
	requestHTTP := flag.String("request-http", envOr("REQUEST_MEDIA_HTTP_URL", "http://127.0.0.1:9380"), "request-media HTTP (search/request)")
	musicHTTP := flag.String("music-http", envOr("MUSIC_HTTP_URL", "http://127.0.0.1:9641"), "media-music HTTP (optional library-plus)")
	booksHTTP := flag.String("books-http", envOr("BOOKS_HTTP_URL", "http://127.0.0.1:9651"), "media-books HTTP (optional library-plus)")
	comicsHTTP := flag.String("comics-http", envOr("COMICS_HTTP_URL", "http://127.0.0.1:9661"), "media-comics HTTP (optional library-plus)")
	audiobooksHTTP := flag.String("audiobooks-http", envOr("AUDIOBOOKS_HTTP_URL", "http://127.0.0.1:9671"), "media-audiobooks HTTP (optional library-plus)")
	authHTTP := flag.String("auth-http", envOr("AUTH_HTTP_URL", "http://127.0.0.1:9401"), "browser-facing auth-local URL (login redirects)")
	authInternal := flag.String("auth-http-internal", envOr("AUTH_HTTP_INTERNAL_URL", ""), "server-side auth-local URL for code exchange (defaults to auth-http)")
	publicURL := flag.String("public-url", envOr("MEDIA_UI_PUBLIC_URL", ""), "public origin for OAuth callbacks (e.g. https://media.gringotts)")
	requireAuth := flag.Bool("require-auth", envOr("MEDIA_UI_REQUIRE_AUTH", "1") != "0", "require auth-local login")
	flag.Parse()
	if *dist == "" {
		fmt.Fprintln(os.Stderr, "-dist / MEDIA_UI_DIST is required (path to media-ui/ui/dist-app)")
		os.Exit(1)
	}
	if st, err := os.Stat(*dist); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "dist dir missing: %s (%v)\n", *dist, err)
		os.Exit(1)
	}

	moviesConn, err := grpc.NewClient(*moviesGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial movies: %v", err)
	}
	defer moviesConn.Close()
	tvConn, err := grpc.NewClient(*tvGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial tv: %v", err)
	}
	defer tvConn.Close()
	jellyfinConn, err := grpc.NewClient(*jellyfinGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial jellyfin: %v", err)
	}
	defer jellyfinConn.Close()

	authPublic := strings.TrimRight(*authHTTP, "/")
	authInt := strings.TrimRight(*authInternal, "/")
	if authInt == "" {
		authInt = authPublic
	}
	s := &server{
		movies:         mgmntv1.NewMovieManagementServiceClient(moviesConn),
		tv:             tvmgmtv1.NewTvManagementServiceClient(tvConn),
		jellyfin:       jellyfinv1.NewJellyfinBridgeClient(jellyfinConn),
		moviesHTTP:     mustURL(*moviesHTTP),
		tvHTTP:         mustURL(*tvHTTP),
		requestHTTP:    mustURL(*requestHTTP),
		musicHTTP:      mustURL(*musicHTTP),
		booksHTTP:      mustURL(*booksHTTP),
		comicsHTTP:     mustURL(*comicsHTTP),
		audiobooksHTTP: mustURL(*audiobooksHTTP),
		authHTTP:       authPublic,
		authInternal:   authInt,
		publicURL:      strings.TrimRight(*publicURL, "/"),
		dist:           *dist,
		requireAuth:    *requireAuth,
		sessions:       newSessionStore(24 * time.Hour),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/auth/callback", s.handleAuthCallback)
	mux.HandleFunc("/logout", s.handleLogout)

	mux.HandleFunc("/api/movies", s.handleListMovies)
	mux.HandleFunc("/api/movies/", s.handleMovieByID)
	mux.HandleFunc("/api/tv", s.handleListTV)
	mux.HandleFunc("/api/tv/", s.handleTVByID)
	s.registerLibraryRoutes(mux)
	mux.HandleFunc("/api/jellyfin/play", s.handleJellyfinPlay)
	reqProxy := reverseProxy(s.requestHTTP)
	mux.Handle("/api/search", reqProxy)
	mux.Handle("/api/request", reqProxy)
	mux.Handle("/api/requests", reqProxy)
	// SPA uses /images/movies/<rel> and /images/tv/<rel>; modules serve under /images/<rel>.
	mux.Handle("/images/movies/", imagePrefixProxy("/images/movies", "/images", reverseProxy(s.moviesHTTP)))
	mux.Handle("/images/tv/", imagePrefixProxy("/images/tv", "/images", reverseProxy(s.tvHTTP)))
	mux.Handle("/stream/movies/", reverseProxy(s.moviesHTTP))
	mux.Handle("/stream/tv/", reverseProxy(s.tvHTTP))
	mux.HandleFunc("/", s.spa)

	handler := http.Handler(mux)
	if s.requireAuth {
		handler = s.withAuth(mux)
	}

	log.Printf("media-ui proxy listening on %s (dist=%s auth=%v auth_http=%s auth_internal=%s public=%s)",
		*listen, *dist, *requireAuth, s.authHTTP, s.authInternal, s.publicURL)
	if err := http.ListenAndServe(*listen, handler); err != nil {
		log.Fatal(err)
	}
}

type sessionStore struct {
	mu   sync.Mutex
	ttl  time.Duration
	byID map[string]sessionEntry
}

type sessionEntry struct {
	userID   string
	username string
	expiry   time.Time
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, byID: make(map[string]sessionEntry)}
}

func (s *sessionStore) Create(userID, username string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b[:])
	s.mu.Lock()
	s.byID[tok] = sessionEntry{userID: userID, username: username, expiry: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return tok, nil
}

func (s *sessionStore) Valid(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.byID[tok]
	if !ok {
		return false
	}
	if time.Now().After(e.expiry) {
		delete(s.byID, tok)
		return false
	}
	return true
}

func (s *sessionStore) Delete(tok string) {
	s.mu.Lock()
	delete(s.byID, tok)
	s.mu.Unlock()
}

type server struct {
	movies         mgmntv1.MovieManagementServiceClient
	tv             tvmgmtv1.TvManagementServiceClient
	jellyfin       jellyfinv1.JellyfinBridgeClient
	moviesHTTP     *url.URL
	tvHTTP         *url.URL
	requestHTTP    *url.URL
	musicHTTP      *url.URL
	booksHTTP      *url.URL
	comicsHTTP     *url.URL
	audiobooksHTTP *url.URL
	authHTTP       string // browser redirects
	authInternal   string // server-side code exchange
	publicURL      string // optional fixed public origin
	dist           string
	requireAuth    bool
	sessions       *sessionStore
}

func (s *server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/login", "/auth/callback", "/logout":
			next.ServeHTTP(w, r)
			return
		}
		// Poster/backdrop URLs are loaded via <img>; allow after login path rewrite
		// without forcing a login redirect (cookies are still preferred for /api).
		if strings.HasPrefix(r.URL.Path, "/images/") {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie("session")
		if err == nil && s.sessions.Valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if wantsJSON(r) || strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/stream/") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.redirectLogin(w, r)
	})
}

func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json")
}

func (s *server) redirectLogin(w http.ResponseWriter, r *http.Request) {
	callback := s.publicOrigin(r) + "/auth/callback"
	target := s.authHTTP + "/login?redirect=" + url.QueryEscape(callback)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.redirectLogin(w, r)
}

func (s *server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "code required", http.StatusBadRequest)
		return
	}
	body, _ := json.Marshal(map[string]string{"code": code})
	// Exchange must hit auth over the loopback URL — browsers use AUTH_HTTP_URL (Caddy),
	// but the host often cannot resolve/trust https://auth.*.
	resp, err := http.Post(s.authInternal+"/login/exchange", "application/json", strings.NewReader(string(body)))
	if err != nil {
		log.Printf("auth exchange: %v (internal=%s)", err, s.authInternal)
		http.Error(w, "auth unavailable", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "code exchange failed", http.StatusUnauthorized)
		return
	}
	var result struct {
		Token    string `json:"token"`
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "invalid response", http.StatusInternalServerError)
		return
	}
	sess, err := s.sessions.Create(result.UserID, result.Username)
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	origin := s.publicOrigin(r)
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sess,
		Path:     "/",
		HttpOnly: true,
		Secure:   strings.HasPrefix(origin, "https://"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("session"); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *server) publicOrigin(r *http.Request) string {
	if s.publicURL != "" {
		return s.publicURL
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		host = "127.0.0.1:5173"
	}
	return scheme + "://" + host
}

func (s *server) spa(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/images/") || strings.HasPrefix(r.URL.Path, "/stream/") {
		http.NotFound(w, r)
		return
	}
	p := path.Clean("/" + r.URL.Path)
	fsPath := path.Join(s.dist, p)
	if p != "/" {
		if st, err := os.Stat(fsPath); err == nil && !st.IsDir() {
			http.ServeFile(w, r, fsPath)
			return
		}
	}
	http.ServeFile(w, r, path.Join(s.dist, "index.html"))
}

func (s *server) handleListMovies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, pageSize := pageParams(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.movies.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Page: page, PageSize: pageSize})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	items := make([]map[string]any, 0, len(resp.GetMovies()))
	for _, m := range resp.GetMovies() {
		items = append(items, movieJSON(m))
	}
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     resp.GetTotal(),
		"page":      resp.GetPage(),
		"page_size": resp.GetPageSize(),
	})
}

func (s *server) handleMovieByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/movies/")
	id = strings.Trim(id, "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.movies.GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: id})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"movie": movieJSON(resp.GetMovie())})
}

func (s *server) handleListTV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, pageSize := pageParams(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.tv.ListTVShows(ctx, &tvmgmtv1.ListTVShowsRequest{Page: page, PageSize: pageSize})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	items := make([]map[string]any, 0, len(resp.GetSeries()))
	for _, m := range resp.GetSeries() {
		items = append(items, tvJSON(m))
	}
	writeJSON(w, map[string]any{
		"items":     items,
		"total":     resp.GetTotal(),
		"page":      resp.GetPage(),
		"page_size": resp.GetPageSize(),
	})
}

func (s *server) handleTVByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/tv/")
	id = strings.Trim(id, "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.tv.GetTVShow(ctx, &tvmgmtv1.GetTVShowRequest{SeriesId: id})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"show": tvJSON(resp.GetSeries())})
}

// handleJellyfinPlay resolves mux_id → Jellyfin item link → play deep-link URL.
// SPA: GET /api/jellyfin/play?mux_id=… → {"url":"…"}. 404 when unlinked; 503 when bridge down.
func (s *server) handleJellyfinPlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	muxID := strings.TrimSpace(r.URL.Query().Get("mux_id"))
	if muxID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "mux_id required", "code": "jellyfin.mux_id_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	list, err := s.jellyfin.ListItemLinks(ctx, &jellyfinv1.ListItemLinksRequest{})
	if err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "jellyfin.unavailable"})
		return
	}
	var jfID string
	for _, link := range list.GetLinks() {
		if link.GetMuxcoreId() == muxID && link.GetJellyfinId() != "" {
			jfID = link.GetJellyfinId()
			break
		}
	}
	if jfID == "" {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "no jellyfin link for mux_id", "code": "jellyfin.not_linked"})
		return
	}

	play, err := s.jellyfin.PlayURL(ctx, &jellyfinv1.PlayURLRequest{ItemId: jfID})
	if err != nil {
		// Soft fallback when bridge has a link but PlayURL needs a configured base URL.
		st, stErr := s.jellyfin.Status(ctx, &jellyfinv1.StatusRequest{})
		if stErr != nil {
			writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "code": "jellyfin.play_failed"})
			return
		}
		if st.GetBaseUrl() == "" {
			writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "jellyfin not configured", "code": "jellyfin.not_configured"})
			return
		}
		u := strings.TrimRight(st.GetBaseUrl(), "/") + "/web/index.html#!/details?id=" + url.PathEscape(jfID)
		writeJSON(w, map[string]any{"url": u})
		return
	}
	if play.GetUrl() == "" {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "empty play url", "code": "jellyfin.empty_url"})
		return
	}
	writeJSON(w, map[string]any{"url": play.GetUrl()})
}

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func movieJSON(m *mgmntv1.MovieItem) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	genres := m.GetGenres()
	if genres == nil {
		genres = []string{}
	}
	out := map[string]any{
		"id":           m.GetId(),
		"tmdb_id":      m.GetTmdbId(),
		"title":        m.GetTitle(),
		"year":         m.GetYear(),
		"overview":     m.GetOverview(),
		"runtime":      m.GetRuntime(),
		"vote_average": m.GetVoteAverage(),
		"genres":       genres,
		"poster_url":   consumerImageURL("movies", firstNonEmpty(m.GetPosterUrl(), m.GetPosterPath())),
		"backdrop_url": consumerImageURL("movies", firstNonEmpty(m.GetBackdropUrl(), m.GetBackdropPath())),
		"has_file":     m.GetHasFile(),
		"status":       m.GetStatus(),
		"tagline":      m.GetTagline(),
		"created_at":   m.GetCreatedAt(),
	}
	if m.GetHasFile() && m.GetId() != "" {
		out["stream_url"] = "/stream/movies/" + url.PathEscape(m.GetId())
	}
	return out
}

func tvJSON(m *tvmgmtv1.TVSeries) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	genres := m.GetGenres()
	if genres == nil {
		genres = []string{}
	}
	hasFile := false
	streamURL := ""
	seasons := make([]map[string]any, 0, len(m.GetSeasons()))
	for _, season := range m.GetSeasons() {
		eps := make([]map[string]any, 0, len(season.GetEpisodes()))
		for _, ep := range season.GetEpisodes() {
			epStream := ""
			if ep.GetHasFile() {
				hasFile = true
				epStream = "/stream/tv/" + url.PathEscape(ep.GetId())
				if streamURL == "" {
					streamURL = epStream
				}
			}
			eps = append(eps, map[string]any{
				"id":             ep.GetId(),
				"season_number":  ep.GetSeasonNumber(),
				"episode_number": ep.GetEpisodeNumber(),
				"title":          ep.GetName(),
				"overview":       ep.GetOverview(),
				"air_date":       ep.GetAirDate(),
				"has_file":       ep.GetHasFile(),
				"stream_url":     epStream,
			})
		}
		seasons = append(seasons, map[string]any{
			"id":            season.GetId(),
			"season_number": season.GetSeasonNumber(),
			"name":          season.GetName(),
			"episode_count": season.GetEpisodeCount(),
			"poster_url":    consumerImageURL("tv", season.GetPosterPath()),
			"episodes":      eps,
		})
	}
	return map[string]any{
		"id":           m.GetId(),
		"tmdb_id":      m.GetTmdbId(),
		"title":        m.GetName(),
		"name":         m.GetName(),
		"year":         m.GetYear(),
		"overview":     m.GetOverview(),
		"vote_average": m.GetVoteAverage(),
		"genres":       genres,
		"poster_url":   consumerImageURL("tv", firstNonEmpty(m.GetPosterUrl(), m.GetPosterPath())),
		"backdrop_url": consumerImageURL("tv", firstNonEmpty(m.GetBackdropUrl(), m.GetBackdropPath())),
		"has_file":     hasFile,
		"stream_url":   streamURL,
		"status":       m.GetStatus(),
		"created_at":   m.GetCreatedAt(),
		"seasons":      seasons,
	}
}

// consumerImageURL rewrites module-relative artwork paths to SPA-facing /images/{movies|tv}/… URLs.
func consumerImageURL(kind, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "/images/") || strings.HasPrefix(raw, "/stream/") {
		return raw
	}
	raw = strings.TrimPrefix(raw, "/")
	if kind == "tv" {
		return "/images/tv/" + raw
	}
	return "/images/movies/" + raw
}

func pageParams(r *http.Request) (int32, int32) {
	page := int32(1)
	pageSize := int32(48)
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = int32(n)
		}
	}
	if v := r.URL.Query().Get("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pageSize = int32(n)
		}
	}
	return page, pageSize
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func reverseProxy(target *url.URL) http.Handler {
	p := httputil.NewSingleHostReverseProxy(target)
	orig := p.Director
	p.Director = func(r *http.Request) {
		orig(r)
		r.Host = target.Host
	}
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
	return p
}

// imagePrefixProxy rewrites /images/movies/<rel> → /images/<rel> (same for tv)
// before handing off to the module reverse proxy.
func imagePrefixProxy(publicPrefix, modulePrefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		suffix := strings.TrimPrefix(r.URL.Path, publicPrefix)
		if suffix == r.URL.Path {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(suffix, "/") {
			suffix = "/" + suffix
		}
		r2 := r.Clone(r.Context())
		u := *r.URL
		u.Path = modulePrefix + suffix
		u.RawPath = ""
		r2.URL = &u
		r2.RequestURI = ""
		next.ServeHTTP(w, r2)
	})
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		log.Fatalf("bad url %q: %v", raw, err)
	}
	return u
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
