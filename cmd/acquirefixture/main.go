// Command acquirefixture drives the fixture acquisition path end to end
// (roadmap T-M2-03, FR-INS-003, ADR-0008 fixture-only, ADR-0014 release gate):
//
//	ensure movie (media-movies AddMovie) → media-automation AddToQueue →
//	SearchItem (≥1 fixture-indexer match) → Dispatch (status sent + download id) →
//	GetHistory until completed (fail fast on import_failed / failed) →
//	media-movies has_file with the imported file → media-ui BFF /api/movies
//	stream_url + range GET + /api/playback/resolve.
//
// Only fixture releases are ever dispatched: a match is eligible only when its
// indexer name contains "fixture" (indexer-piratebay INDEXER_FIXTURE=1), and the
// downloader is expected to run DOWNLOADER_ENGINE=fixture. No live indexer,
// swarm, or paid service is contacted by this command.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// fixtureMarker is the token indexer-piratebay puts in fixture-mode indexer
// names (and media-automation's dispatch guard looks for).
const fixtureMarker = "fixture"

type config struct {
	tmdbID int32
	title  string
	year   int32
	root   string

	historyTimeout time.Duration
	fileTimeout    time.Duration
	pollInterval   time.Duration
	rpcTimeout     time.Duration

	// BFF (media-ui) verification; skipped when mediaUI is empty.
	mediaUI    string
	authURL    string
	user       string
	password   string
	requireBFF bool
}

type result struct {
	MovieID     string
	Release     string
	Indexer     string
	DownloadID  string
	HistoryID   string
	FilePath    string
	BFFVerified bool
}

type acquirer struct {
	cfg    config
	auto   automationv1.AutomationServiceClient
	movies mgmntv1.MovieManagementServiceClient
	http   *http.Client
	logf   func(format string, args ...any)
}

func main() {
	moviesAddr := flag.String("movies-addr", "127.0.0.1:9420", "media-movies gRPC address")
	autoAddr := flag.String("automation-addr", "127.0.0.1:9460", "media-automation gRPC address")
	tmdb := flag.Int("tmdb", 550, "TMDB id of the wanted movie")
	title := flag.String("title", "Fight Club", "movie title (SearchItem query)")
	year := flag.Int("year", 1999, "movie year")
	root := flag.String("root", "", "root folder path passed to AddMovie when the movie is created")
	historyTimeout := flag.Duration("history-timeout", 120*time.Second, "max wait for the download to reach completed")
	fileTimeout := flag.Duration("file-timeout", 60*time.Second, "max wait for media-movies has_file after completion")
	poll := flag.Duration("poll", 2*time.Second, "poll interval")
	mediaUI := flag.String("media-ui", "", "media-ui BFF base URL (e.g. http://127.0.0.1:5173); empty skips BFF checks")
	authURL := flag.String("auth-url", "http://127.0.0.1:9401", "auth-local HTTP base URL (BFF login)")
	user := flag.String("user", envOr("MVP_ADMIN_USER", "admin"), "login user for the BFF")
	password := flag.String("password", envOr("MVP_ADMIN_PASSWORD", adminPasswordFromFile()), "login password for the BFF (default: $MVP_ADMIN_PASSWORD or the run-host generated data/auth/admin.password)")
	requireBFF := flag.Bool("require-bff", false, "fail when media-ui is not reachable instead of skipping BFF checks")
	flag.Parse()

	cfg := config{
		tmdbID: int32(*tmdb), //nolint:gosec // TMDB ids fit int32 (proto field type)
		title:  *title,
		year:   int32(*year), //nolint:gosec // year fits int32
		root:   *root,

		historyTimeout: *historyTimeout,
		fileTimeout:    *fileTimeout,
		pollInterval:   *poll,
		rpcTimeout:     15 * time.Second,

		mediaUI:    strings.TrimRight(*mediaUI, "/"),
		authURL:    strings.TrimRight(*authURL, "/"),
		user:       *user,
		password:   *password,
		requireBFF: *requireBFF,
	}

	autoConn, err := grpc.NewClient(*autoAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fail("dial media-automation: %v", err)
	}
	defer func() { _ = autoConn.Close() }()
	moviesConn, err := grpc.NewClient(*moviesAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fail("dial media-movies: %v", err)
	}
	defer func() { _ = moviesConn.Close() }()

	jar, err := cookiejar.New(nil)
	if err != nil {
		fail("cookie jar: %v", err)
	}
	a := &acquirer{
		cfg:    cfg,
		auto:   automationv1.NewAutomationServiceClient(autoConn),
		movies: mgmntv1.NewMovieManagementServiceClient(moviesConn),
		http: &http.Client{
			Timeout: 20 * time.Second,
			Jar:     jar,
			// Login is a POST→303 chain; follow manually so the POST is never replayed.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		logf: func(format string, args ...any) { fmt.Printf(format+"\n", args...) },
	}
	res, err := a.run(context.Background())
	if err != nil {
		fail("%v", err)
	}
	bff := "skipped (media-ui not checked)"
	if res.BFFVerified {
		bff = "stream + playback resolve OK"
	}
	fmt.Printf("OK acquisition fixture: movie=%s release=%q indexer=%q download=%s file=%s bff=%s\n",
		res.MovieID, res.Release, res.Indexer, res.DownloadID, res.FilePath, bff)
}

// adminPasswordFromFile reads the password run-host.sh generated on first run
// (there is no default admin password; FR-INS-004). Empty when absent.
func adminPasswordFromFile() string {
	p := strings.TrimSpace(os.Getenv("MVP_ADMIN_PASSWORD_FILE"))
	if p == "" {
		p = filepath.Join("data", "auth", "admin.password")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL acquisition: "+format+"\n", args...)
	os.Exit(1)
}

func (a *acquirer) rpcCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, a.cfg.rpcTimeout)
}

func (a *acquirer) run(ctx context.Context) (*result, error) {
	res := &result{}

	movieID, err := a.ensureMovie(ctx)
	if err != nil {
		return nil, err
	}
	res.MovieID = movieID
	a.logf("movie id=%s tmdb=%d title=%q", movieID, a.cfg.tmdbID, a.cfg.title)

	before, err := a.fileIDs(ctx, movieID)
	if err != nil {
		return nil, err
	}

	cctx, cancel := a.rpcCtx(ctx)
	q, err := a.auto.AddToQueue(cctx, &automationv1.AddToQueueRequest{
		ItemType: "movie",
		ItemId:   movieID,
		TmdbId:   a.cfg.tmdbID,
		Title:    a.cfg.title,
		Year:     a.cfg.year,
	})
	cancel()
	if err != nil {
		return nil, fmt.Errorf("AddToQueue: %w", err)
	}
	a.logf("queued wanted=%s", q.GetQueueId())

	cctx, cancel = a.rpcCtx(ctx)
	search, err := a.auto.SearchItem(cctx, &automationv1.SearchItemRequest{
		ItemType: "movie",
		Query:    a.cfg.title,
		TmdbId:   a.cfg.tmdbID,
		Year:     a.cfg.year,
		Limit:    25,
	})
	cancel()
	if err != nil {
		return nil, fmt.Errorf("SearchItem: %w", err)
	}
	match, err := pickFixtureMatch(search.GetMatches())
	if err != nil {
		return nil, err
	}
	res.Release, res.Indexer = match.GetTitle(), match.GetIndexerName()
	a.logf("search matches=%d fixture pick=%q indexer=%q score=%d", len(search.GetMatches()), match.GetTitle(), match.GetIndexerName(), match.GetScore())

	cctx, cancel = a.rpcCtx(ctx)
	disp, err := a.auto.Dispatch(cctx, &automationv1.DispatchRequest{
		Guid:             match.GetGuid(),
		Title:            match.GetTitle(),
		DownloadUrl:      match.GetDownloadUrl(),
		DownloadProtocol: match.GetDownloadProtocol(),
		Size:             match.GetSize(),
		Score:            match.GetScore(),
		IndexerName:      match.GetIndexerName(),
		ItemType:         "movie",
		ItemId:           movieID,
		TmdbId:           a.cfg.tmdbID,
	})
	cancel()
	if err != nil {
		return nil, fmt.Errorf("dispatch %q: %w", match.GetTitle(), err)
	}
	if disp.GetStatus() != "sent" || disp.GetDownloadId() == "" {
		return nil, fmt.Errorf("dispatch %q: status=%q download_id=%q (want sent + id)", match.GetTitle(), disp.GetStatus(), disp.GetDownloadId())
	}
	res.DownloadID = disp.GetDownloadId()
	a.logf("dispatched download_id=%s", res.DownloadID)

	rec, err := a.waitCompleted(ctx, res.DownloadID, match.GetGuid())
	if err != nil {
		return nil, err
	}
	res.HistoryID = rec.GetId()
	a.logf("history %s status=%s", rec.GetId(), rec.GetStatus())

	path, err := a.waitHasFile(ctx, movieID, match.GetTitle(), before)
	if err != nil {
		return nil, err
	}
	res.FilePath = path
	a.logf("movie has_file path=%s", path)

	ok, err := a.verifyBFF(ctx, movieID)
	if err != nil {
		return nil, err
	}
	res.BFFVerified = ok
	return res, nil
}

func (a *acquirer) ensureMovie(ctx context.Context) (string, error) {
	if id, err := a.findMovie(ctx); err != nil || id != "" {
		return id, err
	}
	cctx, cancel := a.rpcCtx(ctx)
	add, err := a.movies.AddMovie(cctx, &mgmntv1.AddMovieRequest{
		TmdbId:         a.cfg.tmdbID,
		Title:          a.cfg.title,
		Year:           a.cfg.year,
		RootFolderPath: a.cfg.root,
	})
	cancel()
	if err == nil && add.GetMovieId() != "" {
		return add.GetMovieId(), nil
	}
	// Raced with another writer (or AddMovie is not idempotent): re-list once.
	if id, lerr := a.findMovie(ctx); lerr == nil && id != "" {
		return id, nil
	}
	if err == nil {
		err = errors.New("empty movie_id")
	}
	return "", fmt.Errorf("AddMovie tmdb=%d: %w", a.cfg.tmdbID, err)
}

func (a *acquirer) findMovie(ctx context.Context) (string, error) {
	for page := int32(1); page <= 20; page++ {
		cctx, cancel := a.rpcCtx(ctx)
		list, err := a.movies.ListMovies(cctx, &mgmntv1.ListMoviesRequest{Page: page, PageSize: 100})
		cancel()
		if err != nil {
			return "", fmt.Errorf("ListMovies: %w", err)
		}
		for _, m := range list.GetMovies() {
			if m.GetTmdbId() == a.cfg.tmdbID {
				return m.GetId(), nil
			}
		}
		if len(list.GetMovies()) < 100 || int(page)*100 >= int(list.GetTotal()) {
			return "", nil
		}
	}
	return "", nil
}

func (a *acquirer) fileIDs(ctx context.Context, movieID string) (map[string]bool, error) {
	cctx, cancel := a.rpcCtx(ctx)
	defer cancel()
	files, err := a.movies.ListFiles(cctx, &mgmntv1.ListFilesRequest{MovieId: movieID})
	if err != nil {
		return nil, fmt.Errorf("ListFiles %s: %w", movieID, err)
	}
	ids := make(map[string]bool, len(files.GetFiles()))
	for _, f := range files.GetFiles() {
		ids[f.GetId()] = true
	}
	return ids, nil
}

// pickFixtureMatch returns the best eligible fixture release: indexer name
// contains "fixture", not rejected, highest score then seeders. Live-indexer
// matches are never eligible (ADR-0008).
func pickFixtureMatch(matches []*automationv1.ReleaseMatch) (*automationv1.ReleaseMatch, error) {
	var fixture, eligible []*automationv1.ReleaseMatch
	for _, m := range matches {
		if !strings.Contains(strings.ToLower(m.GetIndexerName()), fixtureMarker) {
			continue
		}
		fixture = append(fixture, m)
		if !m.GetRejected() {
			eligible = append(eligible, m)
		}
	}
	if len(fixture) == 0 {
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, fmt.Sprintf("%q@%q", m.GetTitle(), m.GetIndexerName()))
		}
		return nil, fmt.Errorf("SearchItem: no fixture-indexer match among %d matches [%s] (enable indexer-piratebay with INDEXER_FIXTURE=1)",
			len(matches), strings.Join(names, ", "))
	}
	if len(eligible) == 0 {
		reasons := make([]string, 0, len(fixture))
		for _, m := range fixture {
			reasons = append(reasons, fmt.Sprintf("%q: %s", m.GetTitle(), m.GetRejectionReason()))
		}
		return nil, fmt.Errorf("SearchItem: all %d fixture matches rejected [%s]", len(fixture), strings.Join(reasons, "; "))
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].GetScore() != eligible[j].GetScore() {
			return eligible[i].GetScore() > eligible[j].GetScore()
		}
		return eligible[i].GetSeeders() > eligible[j].GetSeeders()
	})
	return eligible[0], nil
}

// historyOutcome classifies one download record: done (completed), a terminal
// failure, or still pending.
func historyOutcome(rec *automationv1.DownloadRecord) (done bool, err error) {
	switch rec.GetStatus() {
	case "completed":
		return true, nil
	case "import_failed", "failed":
		detail := rec.GetStatusDetail()
		if detail == "" {
			detail = rec.GetStatusLabel()
		}
		return false, fmt.Errorf("download %s (%q) %s: %s", rec.GetDownloadId(), rec.GetTitle(), rec.GetStatus(), detail)
	}
	return false, nil
}

func (a *acquirer) waitCompleted(ctx context.Context, downloadID, guid string) (*automationv1.DownloadRecord, error) {
	deadline := time.Now().Add(a.cfg.historyTimeout)
	last := "not in history"
	for {
		rec, err := a.findHistory(ctx, downloadID, guid)
		if err != nil {
			last = err.Error()
		} else if rec != nil {
			done, ferr := historyOutcome(rec)
			if ferr != nil {
				return nil, ferr
			}
			if done {
				return rec, nil
			}
			last = "status=" + rec.GetStatus()
			if d := rec.GetStatusDetail(); d != "" {
				last += " (" + d + ")"
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("download %s not completed within %s (last: %s)", downloadID, a.cfg.historyTimeout, last)
		}
		if err := sleepCtx(ctx, a.cfg.pollInterval); err != nil {
			return nil, err
		}
	}
}

func (a *acquirer) findHistory(ctx context.Context, downloadID, guid string) (*automationv1.DownloadRecord, error) {
	cctx, cancel := a.rpcCtx(ctx)
	defer cancel()
	hist, err := a.auto.GetHistory(cctx, &automationv1.GetHistoryRequest{Page: 1, PageSize: 100})
	if err != nil {
		return nil, fmt.Errorf("GetHistory: %w", err)
	}
	var byGUID *automationv1.DownloadRecord
	for _, r := range hist.GetRecords() {
		if r.GetDownloadId() == downloadID {
			return r, nil
		}
		if byGUID == nil && guid != "" && r.GetGuid() == guid {
			byGUID = r
		}
	}
	return byGUID, nil
}

// waitHasFile polls until media-movies reports has_file and lists a file that
// is either new since before the dispatch or carries the release name.
func (a *acquirer) waitHasFile(ctx context.Context, movieID, release string, before map[string]bool) (string, error) {
	deadline := time.Now().Add(a.cfg.fileTimeout)
	want := strings.ToLower(release)
	last := ""
	for {
		cctx, cancel := a.rpcCtx(ctx)
		mv, err := a.movies.GetMovie(cctx, &mgmntv1.GetMovieRequest{MovieId: movieID})
		cancel()
		switch {
		case err != nil:
			last = "GetMovie: " + err.Error()
		case !mv.GetMovie().GetHasFile():
			last = "has_file=false"
		default:
			cctx, cancel := a.rpcCtx(ctx)
			files, ferr := a.movies.ListFiles(cctx, &mgmntv1.ListFilesRequest{MovieId: movieID})
			cancel()
			if ferr != nil {
				last = "ListFiles: " + ferr.Error()
				break
			}
			paths := make([]string, 0, len(files.GetFiles()))
			for _, f := range files.GetFiles() {
				p := f.GetFilePath()
				if !before[f.GetId()] || strings.Contains(strings.ToLower(p), want) {
					return p, nil
				}
				paths = append(paths, p)
			}
			last = fmt.Sprintf("has_file=true but no new file / none named %q (files: %s)", release, strings.Join(paths, ", "))
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("movie %s: imported file not visible within %s (last: %s)", movieID, a.cfg.fileTimeout, last)
		}
		if err := sleepCtx(ctx, a.cfg.pollInterval); err != nil {
			return "", err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// verifyBFF logs into media-ui and checks the acquired movie streams. Returns
// false (no error) when media-ui is not configured / not reachable and
// requireBFF is unset.
func (a *acquirer) verifyBFF(ctx context.Context, movieID string) (bool, error) {
	base := a.cfg.mediaUI
	if base == "" {
		if a.cfg.requireBFF {
			return false, errors.New("BFF required but -media-ui is empty")
		}
		return false, nil
	}
	if code, err := a.get(ctx, base+"/healthz", nil); err != nil || code != http.StatusOK {
		if a.cfg.requireBFF {
			return false, fmt.Errorf("media-ui %s/healthz not reachable (code %d, err %v)", base, code, err)
		}
		a.logf("media-ui %s not reachable; skipping BFF checks", base)
		return false, nil
	}
	if err := a.login(ctx); err != nil {
		return false, err
	}

	var list struct {
		Items []struct {
			ID        string `json:"id"`
			TmdbID    int32  `json:"tmdb_id"`
			HasFile   bool   `json:"has_file"`
			StreamURL string `json:"stream_url"`
		} `json:"items"`
	}
	stream := ""
	for page := 1; page <= 20 && stream == ""; page++ {
		list.Items = nil
		if err := a.getJSON(ctx, fmt.Sprintf("%s/api/movies?page=%d&page_size=100", base, page), &list); err != nil {
			return false, err
		}
		for _, it := range list.Items {
			if it.ID == movieID {
				if !it.HasFile || it.StreamURL == "" {
					return false, fmt.Errorf("BFF /api/movies: %s has_file=%v stream_url=%q", movieID, it.HasFile, it.StreamURL)
				}
				stream = it.StreamURL
				break
			}
		}
		if len(list.Items) < 100 {
			break
		}
	}
	if stream == "" {
		return false, fmt.Errorf("BFF /api/movies: movie %s not listed", movieID)
	}

	code, err := a.get(ctx, base+stream, map[string]string{"Range": "bytes=0-1023"})
	if err != nil {
		return false, fmt.Errorf("BFF stream %s: %w", stream, err)
	}
	if code != http.StatusOK && code != http.StatusPartialContent {
		return false, fmt.Errorf("BFF stream %s: HTTP %d (want 200/206)", stream, code)
	}
	a.logf("BFF stream %s HTTP %d", stream, code)

	var resolved struct {
		StreamURL string `json:"stream_url"`
		Mode      string `json:"mode"`
	}
	if err := a.getJSON(ctx, base+"/api/playback/resolve?src="+url.QueryEscape(stream), &resolved); err != nil {
		return false, err
	}
	if resolved.StreamURL == "" {
		return false, fmt.Errorf("BFF /api/playback/resolve: empty stream_url for %s", stream)
	}
	a.logf("BFF playback resolve mode=%s stream_url=%s", resolved.Mode, resolved.StreamURL)
	return true, nil
}

func (a *acquirer) login(ctx context.Context) error {
	callback := a.cfg.mediaUI + "/auth/callback"
	if _, err := a.get(ctx, a.cfg.authURL+"/login?redirect="+url.QueryEscape(callback), nil); err != nil {
		return fmt.Errorf("auth login page: %w", err)
	}
	authU, err := url.Parse(a.cfg.authURL)
	if err != nil {
		return fmt.Errorf("auth url: %w", err)
	}
	csrf := ""
	for _, c := range a.http.Jar.Cookies(authU) {
		if c.Name == "muxcore-auth-csrf" || c.Name == "csrf-token" {
			csrf = c.Value
		}
	}
	if csrf == "" {
		return errors.New("auth login: no CSRF cookie (muxcore-auth-csrf)")
	}
	form := url.Values{
		"username":   {a.cfg.user},
		"password":   {a.cfg.password},
		"csrf_token": {csrf},
		"redirect":   {callback},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.authURL+"/login/password", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("auth login POST: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	loc := resp.Header.Get("Location")
	if (resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound) || loc == "" {
		return fmt.Errorf("auth login POST: HTTP %d location=%q", resp.StatusCode, loc)
	}
	code, err := a.get(ctx, loc, nil)
	if err != nil {
		return fmt.Errorf("media-ui auth callback: %w", err)
	}
	if code != http.StatusOK && code != http.StatusFound && code != http.StatusSeeOther {
		return fmt.Errorf("media-ui auth callback: HTTP %d", code)
	}
	return nil
}

func (a *acquirer) get(ctx context.Context, u string, hdr map[string]string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func (a *acquirer) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d: %.200s", u, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: decode: %w", u, err)
	}
	return nil
}
