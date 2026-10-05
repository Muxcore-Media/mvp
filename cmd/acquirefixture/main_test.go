package main

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const fixtureRelease = "Fight.Club.1999.1080p.BluRay.x264"

// fakeAutomation is an in-process AutomationService: SearchItem returns the
// configured matches, Dispatch records the grab, and GetHistory walks the
// download through `statuses` (one step per poll; the last one sticks).
type fakeAutomation struct {
	automationv1.UnimplementedAutomationServiceServer

	mu         sync.Mutex
	matches    []*automationv1.ReleaseMatch
	statuses   []string
	detail     string
	polls      int
	queued     []*automationv1.AddToQueueRequest
	dispatched []*automationv1.DispatchRequest
	onComplete func()
}

func (f *fakeAutomation) AddToQueue(_ context.Context, req *automationv1.AddToQueueRequest) (*automationv1.AddToQueueResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queued = append(f.queued, req)
	return &automationv1.AddToQueueResponse{QueueId: "w_movie_" + req.GetItemId()}, nil
}

func (f *fakeAutomation) SearchItem(context.Context, *automationv1.SearchItemRequest) (*automationv1.SearchItemResponse, error) {
	return &automationv1.SearchItemResponse{Matches: f.matches}, nil
}

func (f *fakeAutomation) Dispatch(_ context.Context, req *automationv1.DispatchRequest) (*automationv1.DispatchResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatched = append(f.dispatched, req)
	return &automationv1.DispatchResponse{DownloadId: "dl-1", Status: "sent"}, nil
}

func (f *fakeAutomation) GetHistory(context.Context, *automationv1.GetHistoryRequest) (*automationv1.GetHistoryResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.dispatched) == 0 {
		return &automationv1.GetHistoryResponse{}, nil
	}
	i := f.polls
	if i >= len(f.statuses) {
		i = len(f.statuses) - 1
	}
	f.polls++
	st := f.statuses[i]
	if st == "completed" && f.onComplete != nil {
		f.onComplete()
		f.onComplete = nil
	}
	d := f.dispatched[0]
	return &automationv1.GetHistoryResponse{Records: []*automationv1.DownloadRecord{
		// An unrelated older record must not be picked up.
		{Id: "h-old", DownloadId: "dl-0", Status: "import_failed", StatusDetail: "stale"},
		{Id: "h-1", DownloadId: "dl-1", Guid: d.GetGuid(), Title: d.GetTitle(), Status: st, StatusDetail: f.detail},
	}}, nil
}

// fakeMovies is an in-process MovieManagementService holding one movie.
type fakeMovies struct {
	mgmntv1.UnimplementedMovieManagementServiceServer

	mu      sync.Mutex
	movie   *mgmntv1.MovieItem
	files   []*mgmntv1.MovieFile
	added   int
	hasFile bool
}

func (f *fakeMovies) ListMovies(context.Context, *mgmntv1.ListMoviesRequest) (*mgmntv1.ListMoviesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.movie == nil {
		return &mgmntv1.ListMoviesResponse{}, nil
	}
	return &mgmntv1.ListMoviesResponse{Movies: []*mgmntv1.MovieItem{f.movie}, Total: 1}, nil
}

func (f *fakeMovies) AddMovie(_ context.Context, req *mgmntv1.AddMovieRequest) (*mgmntv1.AddMovieResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added++
	f.movie = &mgmntv1.MovieItem{Id: "mv_550", TmdbId: req.GetTmdbId(), Title: req.GetTitle(), Year: req.GetYear()}
	return &mgmntv1.AddMovieResponse{MovieId: f.movie.GetId()}, nil
}

func (f *fakeMovies) GetMovie(context.Context, *mgmntv1.GetMovieRequest) (*mgmntv1.GetMovieResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := &mgmntv1.MovieItem{Id: f.movie.GetId(), TmdbId: f.movie.GetTmdbId(), HasFile: f.hasFile}
	return &mgmntv1.GetMovieResponse{Movie: m}, nil
}

func (f *fakeMovies) ListFiles(context.Context, *mgmntv1.ListFilesRequest) (*mgmntv1.ListFilesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &mgmntv1.ListFilesResponse{Files: f.files}, nil
}

func (f *fakeMovies) importFile(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hasFile = true
	f.files = append(f.files, &mgmntv1.MovieFile{Id: "f-new", MovieId: f.movie.GetId(), FilePath: path})
}

func serve(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func newTestAcquirer(t *testing.T, auto *fakeAutomation, movies *fakeMovies) *acquirer {
	t.Helper()
	autoConn := serve(t, func(s *grpc.Server) { automationv1.RegisterAutomationServiceServer(s, auto) })
	moviesConn := serve(t, func(s *grpc.Server) { mgmntv1.RegisterMovieManagementServiceServer(s, movies) })
	return &acquirer{
		cfg: config{
			tmdbID: 550, title: "Fight Club", year: 1999,
			historyTimeout: 2 * time.Second,
			fileTimeout:    2 * time.Second,
			pollInterval:   5 * time.Millisecond,
			rpcTimeout:     2 * time.Second,
		},
		auto:   automationv1.NewAutomationServiceClient(autoConn),
		movies: mgmntv1.NewMovieManagementServiceClient(moviesConn),
		logf:   t.Logf,
	}
}

func fixtureMatches() []*automationv1.ReleaseMatch {
	return []*automationv1.ReleaseMatch{
		{Guid: "live-1", Title: "Fight.Club.1999.2160p", IndexerName: "Some Live Indexer", Score: 900, DownloadProtocol: "torrent"},
		{Guid: "fixture-12346", Title: "Fight.Club.1999.720p.WEB-DL", IndexerName: "The Pirate Bay (fixture)", Score: 40, DownloadProtocol: "torrent"},
		{Guid: "fixture-12345", Title: fixtureRelease, IndexerName: "The Pirate Bay (fixture)", Score: 80, Seeders: 42, DownloadProtocol: "torrent", DownloadUrl: "magnet:?xt=urn:btih:abc"},
		{Guid: "fixture-99999", Title: "Fight.Club.1999.CAM", IndexerName: "The Pirate Bay (fixture)", Score: 99, Rejected: true, RejectionReason: "quality"},
	}
}

func TestRunSuccess(t *testing.T) {
	movies := &fakeMovies{}
	auto := &fakeAutomation{matches: fixtureMatches(), statuses: []string{"sent", "sent", "completed"}}
	auto.onComplete = func() { movies.importFile("/library/Movies/Fight Club (1999)/" + fixtureRelease + ".mkv") }
	a := newTestAcquirer(t, auto, movies)

	res, err := a.run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if movies.added != 1 {
		t.Fatalf("AddMovie calls = %d, want 1", movies.added)
	}
	if len(auto.queued) != 1 || auto.queued[0].GetItemId() != "mv_550" || auto.queued[0].GetItemType() != "movie" {
		t.Fatalf("AddToQueue = %+v", auto.queued)
	}
	if len(auto.dispatched) != 1 {
		t.Fatalf("Dispatch calls = %d", len(auto.dispatched))
	}
	d := auto.dispatched[0]
	if d.GetGuid() != "fixture-12345" || d.GetItemId() != "mv_550" || d.GetTmdbId() != 550 {
		t.Fatalf("dispatched wrong release: %+v", d)
	}
	if res.DownloadID != "dl-1" || res.HistoryID != "h-1" || !strings.Contains(res.FilePath, fixtureRelease) {
		t.Fatalf("result = %+v", res)
	}
	if res.BFFVerified {
		t.Fatal("BFF should be skipped without -media-ui")
	}

	// Second run: movie already exists, AddMovie is not called again.
	auto.dispatched, auto.polls = nil, 0
	if _, err := a.run(context.Background()); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if movies.added != 1 {
		t.Fatalf("AddMovie on rerun: calls = %d, want 1", movies.added)
	}
}

func TestRunNoFixtureMatch(t *testing.T) {
	movies := &fakeMovies{}
	auto := &fakeAutomation{
		matches:  []*automationv1.ReleaseMatch{{Guid: "live-1", Title: "Fight.Club.1999.1080p", IndexerName: "Live Torznab", Score: 500}},
		statuses: []string{"completed"},
	}
	a := newTestAcquirer(t, auto, movies)
	_, err := a.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no fixture-indexer match") {
		t.Fatalf("err = %v, want no fixture-indexer match", err)
	}
	if len(auto.dispatched) != 0 {
		t.Fatalf("a live match was dispatched: %+v", auto.dispatched)
	}

	auto.matches = nil
	if _, err := a.run(context.Background()); err == nil || !strings.Contains(err.Error(), "among 0 matches") {
		t.Fatalf("empty search: err = %v", err)
	}
}

func TestRunImportFailed(t *testing.T) {
	movies := &fakeMovies{}
	auto := &fakeAutomation{
		matches:  fixtureMatches(),
		statuses: []string{"sent", "import_failed"},
		detail:   "no importable files found",
	}
	a := newTestAcquirer(t, auto, movies)
	start := time.Now()
	_, err := a.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "import_failed") || !strings.Contains(err.Error(), "no importable files found") {
		t.Fatalf("err = %v, want import_failed with detail", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("import_failed did not fail fast (%s)", time.Since(start))
	}
}

func TestRunHistoryTimeout(t *testing.T) {
	auto := &fakeAutomation{matches: fixtureMatches(), statuses: []string{"sent"}}
	a := newTestAcquirer(t, auto, &fakeMovies{})
	a.cfg.historyTimeout = 50 * time.Millisecond
	_, err := a.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not completed within") || !strings.Contains(err.Error(), "status=sent") {
		t.Fatalf("err = %v, want timeout with last status", err)
	}
}

func TestPickFixtureMatchAllRejected(t *testing.T) {
	_, err := pickFixtureMatch([]*automationv1.ReleaseMatch{
		{Title: "x", IndexerName: "TPB (fixture)", Rejected: true, RejectionReason: "below cutoff"},
	})
	if err == nil || !strings.Contains(err.Error(), "below cutoff") {
		t.Fatalf("err = %v", err)
	}
}

func TestReleaseMatches(t *testing.T) {
	cases := []struct {
		path, release string
		want          bool
	}{
		{"/lib/Movies/Fight Club (1999)/Fight Club (1999) [720p.WEB-DL].mkv", "Fight.Club.1999.720p.WEB-DL", true},
		{"/lib/Movies/Fight Club (1999)/Fight.Club.1999.1080p.BluRay.x264.mkv", "Fight.Club.1999.1080p.BluRay.x264", true},
		{"/lib/Movies/Fight Club (1999)/Fight Club (1999) [1080p.BluRay].mkv", "Fight.Club.1999.720p.WEB-DL", false},
		{"/lib/Movies/Fight Club (1999)/Fight Club (1999).mkv", "", false},
	}
	for _, tc := range cases {
		if got := releaseMatches(tc.path, tc.release); got != tc.want {
			t.Errorf("releaseMatches(%q, %q) = %v, want %v", tc.path, tc.release, got, tc.want)
		}
	}
}
