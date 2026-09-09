package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	backupv1 "github.com/Muxcore-Media/backup-local/muxcore/backup/v1"
	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	indexerv1 "github.com/Muxcore-Media/contracts-indexer/muxcore/indexer/v1"
	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	metadatav1 "github.com/Muxcore-Media/contracts-metadata/muxcore/metadata/v1"
	notifyv1 "github.com/Muxcore-Media/contracts-notification/muxcore/notification/v1"
	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"
	ffprobev1 "github.com/Muxcore-Media/media-ffprobe/proto/ffprobev1"
	introoutrov1 "github.com/Muxcore-Media/media-intro-outro/proto/gen/muxcore/introoutro/v1"
	maintainv1 "github.com/Muxcore-Media/media-library-maintainer/proto/maintainv1"
	listsyncv1 "github.com/Muxcore-Media/media-list-sync/proto/listsyncv1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	plexv1 "github.com/Muxcore-Media/plex/proto/plexv1"
)

func main() {
	listen := flag.String("listen", envOr("MEDIA_UI_LISTEN", ":5173"), "HTTP listen address")
	dist := flag.String("dist", envOr("MEDIA_UI_DIST", ""), "path to media-ui dist-app (required)")
	moviesGRPC := flag.String("movies-grpc", envOr("MOVIES_GRPC_CLIENT_ADDR", "127.0.0.1:9420"), "media-movies gRPC")
	tvGRPC := flag.String("tv-grpc", envOr("TVSHOWS_GRPC_CLIENT_ADDR", "127.0.0.1:9440"), "media-tvshows gRPC")
	jellyfinGRPC := flag.String("jellyfin-grpc", envOr("JELLYFIN_GRPC_CLIENT_ADDR", "127.0.0.1:9475"), "jellyfin bridge gRPC")
	plexGRPC := flag.String("plex-grpc", envOr("PLEX_GRPC_CLIENT_ADDR", "127.0.0.1:9476"), "plex bridge gRPC (optional household session stop)")
	notifyGRPC := flag.String("notify-grpc", envOr("NOTIFY_GRPC_CLIENT_ADDR", "127.0.0.1:9441"), "notification-default gRPC (optional household Connect)")
	backupGRPC := flag.String("backup-grpc", envOr("BACKUP_GRPC_CLIENT_ADDR", "127.0.0.1:9302"), "backup-local gRPC (optional household backups)")
	maintainerGRPC := flag.String("maintainer-grpc", envOr("MAINTAINER_GRPC_CLIENT_ADDR", "127.0.0.1:9545"), "media-library-maintainer gRPC (optional household cleanup)")
	playbackGuardGRPC := flag.String("playback-guard-grpc", envOr("PLAYBACK_GUARD_GRPC_CLIENT_ADDR", "127.0.0.1:9561"), "playback-guard gRPC (optional household session rules)")
	backupRestoreDir := flag.String("backup-restore-dir", envOr("BACKUP_RESTORE_DIR", ""), "configured restore directory on backup-local (never accept a client path)")
	moviesHTTP := flag.String("movies-http", envOr("MOVIES_HTTP_URL", "http://127.0.0.1:9430"), "media-movies HTTP (images/stream)")
	tvHTTP := flag.String("tv-http", envOr("TVSHOWS_HTTP_URL", "http://127.0.0.1:9450"), "media-tvshows HTTP (images)")
	requestHTTP := flag.String("request-http", envOr("REQUEST_MEDIA_HTTP_URL", "http://127.0.0.1:9380"), "request-media HTTP (search/request)")
	musicHTTP := flag.String("music-http", envOr("MUSIC_HTTP_URL", "http://127.0.0.1:9641"), "media-music HTTP (optional library-plus)")
	musicGRPC := flag.String("music-grpc", envOr("MUSIC_GRPC_CLIENT_ADDR", "127.0.0.1:9640"), "media-music gRPC (optional household Lidarr migrate)")
	booksHTTP := flag.String("books-http", envOr("BOOKS_HTTP_URL", "http://127.0.0.1:9651"), "media-books HTTP (optional library-plus)")
	booksGRPC := flag.String("books-grpc", envOr("BOOKS_GRPC_CLIENT_ADDR", "127.0.0.1:9650"), "media-books gRPC (optional household author history)")
	comicsHTTP := flag.String("comics-http", envOr("COMICS_HTTP_URL", "http://127.0.0.1:9661"), "media-comics HTTP (optional library-plus)")
	audiobooksHTTP := flag.String("audiobooks-http", envOr("AUDIOBOOKS_HTTP_URL", "http://127.0.0.1:9671"), "media-audiobooks HTTP (optional library-plus)")
	transcoderHTTP := flag.String("transcoder-http", envOr("TRANSCODER_HTTP_URL", "http://127.0.0.1:9526"), "media-transcoder playback HTTP (on-the-fly transcode)")
	debridHTTP := flag.String("debrid-http", envOr("DEBRID_HTTP_URL", "http://127.0.0.1:9631"), "downloader-debrid health HTTP (optional)")
	indexerPiratebayHTTP := flag.String("indexer-piratebay-http", envOr("INDEXER_PIRATEBAY_HTTP_URL", "http://127.0.0.1:9487"), "indexer-piratebay health HTTP (optional)")
	indexerGRPC := flag.String("indexer-grpc", envOr("INDEXER_TORZNAB_GRPC_CLIENT_ADDR", "127.0.0.1:9486"), "indexer-torznab gRPC (optional Prowlarr/Jackett catalog)")
	downloaderTorrentHTTP := flag.String("downloader-torrent-http", envOr("DOWNLOADER_TORRENT_HTTP_URL", "http://127.0.0.1:9464"), "downloader-native-torrent health HTTP (optional)")
	downloaderQbitHTTP := flag.String("downloader-qbit-http", envOr("DOWNLOADER_QBIT_HTTP_URL", "http://127.0.0.1:9463"), "downloader-qbittorrent health HTTP (optional)")
	downloaderSabHTTP := flag.String("downloader-sab-http", envOr("DOWNLOADER_SAB_HTTP_URL", "http://127.0.0.1:9621"), "downloader-sabnzbd health HTTP (optional)")
	downloaderUsenetHTTP := flag.String("downloader-usenet-http", envOr("DOWNLOADER_USENET_HTTP_URL", "http://127.0.0.1:9623"), "downloader-native-usenet health HTTP (optional)")
	graphHTTP := flag.String("graph-http", envOr("GRAPH_HTTP_URL", "http://127.0.0.1:9731"), "media-graph health/admin HTTP (optional related titles)")
	graphToken := flag.String("graph-token", envOr("GRAPH_MODULE_TOKEN", envOr("GRAPH_HTTP_TOKEN", "")), "optional admin token for media-graph /api/graph*")
	subtitlesGRPC := flag.String("subtitles-grpc", envOr("SUBTITLES_GRPC_CLIENT_ADDR", "127.0.0.1:9520"), "media-subtitles gRPC (optional)")
	subtitlesHTTP := flag.String("subtitles-http", envOr("SUBTITLES_HTTP_URL", "http://127.0.0.1:9521"), "media-subtitles HTTP (optional subtitle files)")
	metadataGRPC := flag.String("metadata-grpc", envOr("METADATA_TMDB_GRPC_ADDR", "127.0.0.1:9411"), "metadata-tmdb gRPC")
	listSyncGRPC := flag.String("listsync-grpc", envOr("LISTSYNC_GRPC_CLIENT_ADDR", "127.0.0.1:9530"), "media-list-sync gRPC (optional watchlist)")
	introOutroGRPC := flag.String("intro-outro-grpc", envOr("INTRO_OUTRO_GRPC_CLIENT_ADDR", "127.0.0.1:9710"), "media-intro-outro gRPC (optional intro/outro/credits skip segments)")
	ffprobeGRPC := flag.String("ffprobe-grpc", envOr("FFPROBE_GRPC_CLIENT_ADDR", "127.0.0.1:9480"), "media-ffprobe gRPC (optional chapter markers)")
	automationGRPC := flag.String("automation-grpc", envOr("AUTOMATION_GRPC_CLIENT_ADDR", "127.0.0.1:9460"), "media-automation gRPC (optional interactive search)")
	scannerGRPC := flag.String("scanner-grpc", envOr("SCANNER_GRPC_CLIENT_ADDR", "127.0.0.1:9470"), "media-scanner gRPC (optional household manual import)")
	formatsGRPC := flag.String("formats-grpc", envOr("FORMATS_GRPC_CLIENT_ADDR", "127.0.0.1:9490"), "media-custom-formats gRPC (optional TRaSH/quality profiles)")
	rootsGRPC := flag.String("roots-grpc", envOr("ROOTS_GRPC_CLIENT_ADDR", "127.0.0.1:9540"), "media-root-folders gRPC (optional household root picker)")
	renameGRPC := flag.String("rename-grpc", envOr("RENAME_GRPC_CLIENT_ADDR", "127.0.0.1:9510"), "media-rename gRPC (optional household Preview Rename)")
	playbackMonitorHTTP := flag.String("playback-monitor-http", envOr("PLAYBACK_MONITOR_HTTP_URL", "http://127.0.0.1:8560"), "playback-monitor HTTP (optional native session ingest)")
	playbackMonitorToken := flag.String("playback-monitor-token", envOr("PLAYBACK_MONITOR_HTTP_TOKEN", ""), "operator token for playback-monitor POST /ingest")
	taggingHTTP := flag.String("tagging-http", envOr("TAGGING_HTTP_URL", "http://127.0.0.1:9741"), "media-tagging HTTP (optional Arr auto-tag rules)")
	authHTTP := flag.String("auth-http", envOr("AUTH_HTTP_URL", "http://127.0.0.1:9401"), "browser-facing auth-local URL (login redirects)")
	authInternal := flag.String("auth-http-internal", envOr("AUTH_HTTP_INTERNAL_URL", ""), "server-side auth-local URL for code exchange (defaults to auth-http)")
	publicURL := flag.String("public-url", envOr("MEDIA_UI_PUBLIC_URL", ""), "public origin for OAuth callbacks (e.g. https://media.gringotts)")
	requireAuth := flag.Bool("require-auth", envOr("MEDIA_UI_REQUIRE_AUTH", "1") != "0", "require auth-local login")
	userdataDir := flag.String("userdata-dir", envOr("MEDIA_UI_USERDATA_DIR", ""), "durable userdata JSON dir (progress/favorites/prefs)")
	livetvFile := flag.String("livetv-file", envOr("MEDIA_UI_LIVETV_FILE", ""), "Live TV channels/guide JSON (shared with admin-ui ADMIN_UI_LIVETV_FILE)")
	libraryPathsFile := flag.String("library-paths-file", envOr("MEDIA_UI_LIBRARY_PATHS_FILE", ""), "JSON map of musicvideos/homevideos path prefixes")
	passwordResetFile := flag.String("password-reset-file", envOr("MEDIA_UI_PASSWORD_RESET_FILE", ""), "password reset request JSON (shared with admin-ui ADMIN_UI_PASSWORD_RESET_FILE)")
	flag.Parse()
	if *livetvFile == "" && *userdataDir != "" {
		*livetvFile = filepath.Join(*userdataDir, "livetv.json")
	}
	if *libraryPathsFile == "" && *userdataDir != "" {
		*libraryPathsFile = filepath.Join(*userdataDir, "library-paths.json")
	}
	if *dist == "" {
		fmt.Fprintln(os.Stderr, "-dist / MEDIA_UI_DIST is required (path to media-ui/ui/dist-app)")
		os.Exit(1)
	}
	if st, err := os.Stat(*dist); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "dist dir missing: %s (%v)\n", *dist, err)
		os.Exit(1)
	}

	moviesConn, err := dialMeshGRPC(*moviesGRPC)
	if err != nil {
		log.Fatalf("dial movies: %v", err)
	}
	defer func() { _ = moviesConn.Close() }()
	tvConn, err := dialMeshGRPC(*tvGRPC)
	if err != nil {
		log.Fatalf("dial tv: %v", err)
	}
	defer func() { _ = tvConn.Close() }()
	jellyfinConn, err := dialMeshGRPC(*jellyfinGRPC)
	if err != nil {
		log.Fatalf("dial jellyfin: %v", err)
	}
	defer func() { _ = jellyfinConn.Close() }()

	var subtitlesClient subtv1.SubtitleServiceClient
	if addr := strings.TrimSpace(*subtitlesGRPC); addr != "" {
		subtitlesConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial subtitles grpc %s: %v (subtitle tracks disabled)", addr, err)
		} else {
			defer func() { _ = subtitlesConn.Close() }()
			subtitlesClient = subtv1.NewSubtitleServiceClient(subtitlesConn)
		}
	}

	var metadataClient metadatav1.MetadataServiceClient
	if addr := strings.TrimSpace(*metadataGRPC); addr != "" {
		metadataConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial metadata grpc %s: %v (discover details disabled)", addr, err)
		} else {
			defer func() { _ = metadataConn.Close() }()
			metadataClient = metadatav1.NewMetadataServiceClient(metadataConn)
		}
	}

	var listSyncClient listsyncv1.ListSyncServiceClient
	if addr := strings.TrimSpace(*listSyncGRPC); addr != "" {
		listSyncConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial list-sync grpc %s: %v (watchlist disabled)", addr, err)
		} else {
			defer func() { _ = listSyncConn.Close() }()
			listSyncClient = listsyncv1.NewListSyncServiceClient(listSyncConn)
		}
	}

	var introOutroClient introoutrov1.IntroOutroServiceClient
	if addr := strings.TrimSpace(*introOutroGRPC); addr != "" {
		introOutroConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial intro-outro grpc %s: %v (intro/outro skip disabled)", addr, err)
		} else {
			defer func() { _ = introOutroConn.Close() }()
			introOutroClient = introoutrov1.NewIntroOutroServiceClient(introOutroConn)
		}
	}

	var ffprobeClient ffprobev1.AnalysisServiceClient
	if addr := strings.TrimSpace(*ffprobeGRPC); addr != "" {
		ffprobeConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial ffprobe grpc %s: %v (chapter markers disabled)", addr, err)
		} else {
			defer func() { _ = ffprobeConn.Close() }()
			ffprobeClient = ffprobev1.NewAnalysisServiceClient(ffprobeConn)
		}
	}

	var rootsClient rootsv1.RootFolderServiceClient
	if addr := strings.TrimSpace(*rootsGRPC); addr != "" {
		rootsConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial roots grpc %s: %v (household root picker disabled)", addr, err)
		} else {
			defer func() { _ = rootsConn.Close() }()
			rootsClient = rootsv1.NewRootFolderServiceClient(rootsConn)
		}
	}

	var musicClient musicv1.MusicManagementServiceClient
	var musicAdminClient mediaadminv1.MediaAdminServiceClient
	if addr := strings.TrimSpace(*musicGRPC); addr != "" {
		musicConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial music grpc %s: %v (household Lidarr migrate disabled)", addr, err)
		} else {
			defer func() { _ = musicConn.Close() }()
			musicClient = musicv1.NewMusicManagementServiceClient(musicConn)
			musicAdminClient = mediaadminv1.NewMediaAdminServiceClient(musicConn)
		}
	}

	var booksAdminClient mediaadminv1.MediaAdminServiceClient
	if addr := strings.TrimSpace(*booksGRPC); addr != "" {
		booksConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial books grpc %s: %v (household book history disabled)", addr, err)
		} else {
			defer func() { _ = booksConn.Close() }()
			booksAdminClient = mediaadminv1.NewMediaAdminServiceClient(booksConn)
		}
	}

	var plexClient plexv1.PlexBridgeServiceClient
	if addr := strings.TrimSpace(*plexGRPC); addr != "" {
		plexConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial plex grpc %s: %v (household Plex stop disabled)", addr, err)
		} else {
			defer func() { _ = plexConn.Close() }()
			plexClient = plexv1.NewPlexBridgeServiceClient(plexConn)
		}
	}

	var notifyClient notifyv1.NotificationServiceClient
	if addr := strings.TrimSpace(*notifyGRPC); addr != "" {
		notifyConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial notify grpc %s: %v (household Connect disabled)", addr, err)
		} else {
			defer func() { _ = notifyConn.Close() }()
			notifyClient = notifyv1.NewNotificationServiceClient(notifyConn)
		}
	}

	var indexerClient indexerv1.IndexerServiceClient
	if addr := strings.TrimSpace(*indexerGRPC); addr != "" {
		indexerConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial indexer grpc %s: %v (Prowlarr/Jackett catalog disabled)", addr, err)
		} else {
			defer func() { _ = indexerConn.Close() }()
			indexerClient = indexerv1.NewIndexerServiceClient(indexerConn)
		}
	}

	var maintainerClient maintainv1.MaintainerServiceClient
	if addr := strings.TrimSpace(*maintainerGRPC); addr != "" {
		maintainerConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial maintainer grpc %s: %v (household library cleanup disabled)", addr, err)
		} else {
			defer func() { _ = maintainerConn.Close() }()
			maintainerClient = maintainv1.NewMaintainerServiceClient(maintainerConn)
		}
	}

	var playbackGuardClient guardv1.PlaybackGuardServiceClient
	if addr := strings.TrimSpace(*playbackGuardGRPC); addr != "" {
		guardConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial playback-guard grpc %s: %v (household session rules disabled)", addr, err)
		} else {
			defer func() { _ = guardConn.Close() }()
			playbackGuardClient = guardv1.NewPlaybackGuardServiceClient(guardConn)
		}
	}

	var backupClient backupv1.BackupServiceClient
	if addr := strings.TrimSpace(*backupGRPC); addr != "" {
		backupConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial backup grpc %s: %v (household backups disabled)", addr, err)
		} else {
			defer func() { _ = backupConn.Close() }()
			backupClient = backupv1.NewBackupServiceClient(backupConn)
		}
	}

	var renameClient renamev1.RenameServiceClient
	if addr := strings.TrimSpace(*renameGRPC); addr != "" {
		renameConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial rename grpc %s: %v (household Preview Rename disabled)", addr, err)
		} else {
			defer func() { _ = renameConn.Close() }()
			renameClient = renamev1.NewRenameServiceClient(renameConn)
		}
	}

	var formatsClient formatsv1.FormatServiceClient
	if addr := strings.TrimSpace(*formatsGRPC); addr != "" {
		formatsConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial formats grpc %s: %v (TRaSH quality packs disabled)", addr, err)
		} else {
			defer func() { _ = formatsConn.Close() }()
			formatsClient = formatsv1.NewFormatServiceClient(formatsConn)
		}
	}

	var automationClient automationv1.AutomationServiceClient
	if addr := strings.TrimSpace(*automationGRPC); addr != "" {
		automationConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial automation grpc %s: %v (interactive search disabled)", addr, err)
		} else {
			defer func() { _ = automationConn.Close() }()
			automationClient = automationv1.NewAutomationServiceClient(automationConn)
		}
	}

	var scannerClient scannerv1.ScannerServiceClient
	if addr := strings.TrimSpace(*scannerGRPC); addr != "" {
		scannerConn, err := dialMeshGRPC(addr)
		if err != nil {
			log.Printf("warn: dial scanner grpc %s: %v (manual import disabled)", addr, err)
		} else {
			defer func() { _ = scannerConn.Close() }()
			scannerClient = scannerv1.NewScannerServiceClient(scannerConn)
		}
	}

	authPublic := strings.TrimRight(*authHTTP, "/")
	authInt := strings.TrimRight(*authInternal, "/")
	if authInt == "" {
		authInt = authPublic
	}
	s := &server{
		movies:           mgmntv1.NewMovieManagementServiceClient(moviesConn),
		moviesAdmin:      mediaadminv1.NewMediaAdminServiceClient(moviesConn),
		tv:               tvmgmtv1.NewTvManagementServiceClient(tvConn),
		tvAdmin:          mediaadminv1.NewMediaAdminServiceClient(tvConn),
		music:            musicClient,
		musicAdmin:       musicAdminClient,
		booksAdmin:       booksAdminClient,
		jellyfin:         jellyfinv1.NewJellyfinBridgeClient(jellyfinConn),
		plex:             plexClient,
		notify:           notifyClient,
		backup:           backupClient,
		maintainer:       maintainerClient,
		playbackGuard:    playbackGuardClient,
		backupRestoreDir: strings.TrimSpace(*backupRestoreDir),
		indexer:          indexerClient,
		moviesHTTP:       mustURL(*moviesHTTP),
		tvHTTP:           mustURL(*tvHTTP),
		requestHTTP:      mustURL(*requestHTTP),
		musicHTTP:        mustURL(*musicHTTP),
		booksHTTP:        mustURL(*booksHTTP),
		comicsHTTP:       mustURL(*comicsHTTP),
		audiobooksHTTP:   mustURL(*audiobooksHTTP),
		transcoderHTTP:   optionalURL(*transcoderHTTP),
		debridHTTP:       optionalURL(*debridHTTP),
		acquisitionPeers: []acquisitionPeerDef{
			optionalAcquisitionPeer("indexer-piratebay", "indexer", "Pirate Bay indexer", *indexerPiratebayHTTP),
			optionalAcquisitionPeer("downloader-native-torrent", "downloader", "Native torrent", *downloaderTorrentHTTP),
			optionalAcquisitionPeer("downloader-qbittorrent", "downloader", "qBittorrent", *downloaderQbitHTTP),
			optionalAcquisitionPeer("downloader-sabnzbd", "downloader", "SABnzbd", *downloaderSabHTTP),
			optionalAcquisitionPeer("downloader-native-usenet", "downloader", "Native usenet", *downloaderUsenetHTTP),
			optionalAcquisitionPeer("downloader-debrid", "downloader", "Debrid", *debridHTTP),
		},
		graphHTTP:            optionalURL(*graphHTTP),
		graphToken:           strings.TrimSpace(*graphToken),
		playbackMonitorHTTP:  optionalURL(*playbackMonitorHTTP),
		playbackMonitorToken: strings.TrimSpace(*playbackMonitorToken),
		taggingHTTP:          optionalURL(*taggingHTTP),
		subtitles:            subtitlesClient,
		subtitlesHTTP:        optionalURL(*subtitlesHTTP),
		metadata:             metadataClient,
		listSync:             listSyncClient,
		introOutro:           introOutroClient,
		ffprobe:              ffprobeClient,
		automation:           automationClient,
		scanner:              scannerClient,
		formats:              formatsClient,
		roots:                rootsClient,
		rename:               renameClient,
		authHTTP:             authPublic,
		authInternal:         authInt,
		publicURL:            strings.TrimRight(*publicURL, "/"),
		trustedProxies:       trustedProxiesFromEnv(),
		dist:                 *dist,
		requireAuth:          *requireAuth,
		sessions:             newSessionStore(24 * time.Hour),
		userdata:             newServerUserdata(*userdataDir),
		livetv:               newLiveTVStore(*livetvFile, *userdataDir),
		libraryPaths:         newLibraryPathsStore(*libraryPathsFile, *userdataDir),
		quickconnect:         newQuickConnectStore(*userdataDir),
		passwordResets:       newPasswordResetStore(*passwordResetFile, *userdataDir),
		issues:               newMediaIssueStore(*userdataDir),
		together:             newWatchTogetherStore(*userdataDir),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/auth/callback", s.handleAuthCallback)
	mux.HandleFunc("/logout", s.handleLogout)

	mux.HandleFunc("GET /api/capabilities", s.handleCapabilities)
	mux.HandleFunc("GET /api/roots", s.handleListRoots)
	mux.HandleFunc("GET /api/roots/browse", s.handleBrowseRoots)
	mux.HandleFunc("GET /api/roots/pick", s.handlePickRoot)
	mux.HandleFunc("POST /api/roots/probe", s.handleProbeRoot)
	mux.HandleFunc("POST /api/roots", s.handleCreateRoot)
	mux.HandleFunc("PATCH /api/roots/{id}", s.handlePatchRoot)
	mux.HandleFunc("DELETE /api/roots/{id}", s.handleDeleteRoot)
	mux.HandleFunc("GET /api/rename/preview", s.handleRenamePreview)
	mux.HandleFunc("POST /api/rename/organize", s.handleOrganizeLibrary)
	mux.HandleFunc("POST /api/rename", s.handleRenameExecute)
	mux.HandleFunc("GET /api/rename/templates", s.handleListRenameTemplates)
	mux.HandleFunc("POST /api/rename/templates", s.handleCreateRenameTemplate)
	mux.HandleFunc("PATCH /api/rename/templates/{id}", s.handlePatchRenameTemplate)
	mux.HandleFunc("DELETE /api/rename/templates/{id}", s.handleDeleteRenameTemplate)
	mux.HandleFunc("GET /api/formats", s.handleFormats)
	mux.HandleFunc("GET /api/formats/release-profiles", s.handleListReleaseProfiles)
	mux.HandleFunc("POST /api/formats/release-profiles", s.handleUpsertReleaseProfile)
	mux.HandleFunc("PATCH /api/formats/release-profiles/{id}", s.handleUpsertReleaseProfile)
	mux.HandleFunc("PUT /api/formats/release-profiles/{id}", s.handleUpsertReleaseProfile)
	mux.HandleFunc("DELETE /api/formats/release-profiles/{id}", s.handleDeleteReleaseProfile)
	mux.HandleFunc("POST /api/formats/sync-trash", s.handleFormatsSyncTrash)
	mux.HandleFunc("POST /api/formats/score", s.handleFormatsScore)
	mux.HandleFunc("POST /api/formats/parse", s.handleFormatsParse)
	mux.HandleFunc("POST /api/formats/profiles", s.handleCreateQualityProfile)
	mux.HandleFunc("PATCH /api/formats/profiles/{id}", s.handlePatchQualityProfile)
	mux.HandleFunc("DELETE /api/formats/profiles/{id}", s.handleDeleteQualityProfile)
	mux.HandleFunc("POST /api/formats", s.handleCreateCustomFormat)
	mux.HandleFunc("PATCH /api/formats/{id}", s.handlePatchCustomFormat)
	mux.HandleFunc("DELETE /api/formats/{id}", s.handleDeleteCustomFormat)
	mux.HandleFunc("GET /api/acquisition", s.handleAcquisition)
	mux.HandleFunc("GET /api/indexers", s.handleListIndexers)
	mux.HandleFunc("POST /api/indexers", s.handleCreateIndexer)
	mux.HandleFunc("PATCH /api/indexers/{id}", s.handleUpdateIndexer)
	mux.HandleFunc("DELETE /api/indexers/{id}", s.handleDeleteIndexer)
	mux.HandleFunc("GET /api/releases/search", s.handleReleaseSearch)
	mux.HandleFunc("POST /api/releases/grab", s.handleReleaseGrab)
	mux.HandleFunc("GET /api/releases/upgrades", s.handleCutoffUnmet)
	mux.HandleFunc("POST /api/releases/search-now", s.handleSearchNow)
	mux.HandleFunc("POST /api/releases/block", s.handleReleaseBlock)
	mux.HandleFunc("GET /api/blocklist", s.handleListBlocklist)
	mux.HandleFunc("POST /api/blocklist/clear", s.handleClearBlocklist)
	mux.HandleFunc("GET /api/delay-profiles", s.handleListDelayProfiles)
	mux.HandleFunc("PUT /api/delay-profiles", s.handleUpsertDelayProfile)
	mux.HandleFunc("POST /api/delay-profiles", s.handleUpsertDelayProfile)
	mux.HandleFunc("GET /api/activity", s.handleActivity)
	mux.HandleFunc("POST /api/activity/retry", s.handleActivityRetry)
	mux.HandleFunc("GET /api/wanted", s.handleWanted)
	mux.HandleFunc("POST /api/wanted", s.handleWantedAdd)
	mux.HandleFunc("GET /api/calendar", s.handleCalendar)
	mux.HandleFunc("GET /api/missing", s.handleLibraryMissing)
	mux.HandleFunc("/api/media-issues", s.handleMediaIssues)
	mux.HandleFunc("/api/watch-together", s.handleWatchTogether)
	mux.HandleFunc("/api/watch-together/", s.handleWatchTogether)
	mux.HandleFunc("POST /api/wanted/remove", s.handleWantedRemove)
	mux.HandleFunc("GET /api/import/candidates", s.handleImportCandidates)
	mux.HandleFunc("POST /api/import", s.handleImportPath)
	mux.HandleFunc("GET /api/scan/watch-dirs", s.handleListWatchDirs)
	mux.HandleFunc("POST /api/scan/watch-dirs", s.handleCreateWatchDir)
	mux.HandleFunc("PATCH /api/scan/watch-dirs/{id}", s.handleUpdateWatchDir)
	mux.HandleFunc("PUT /api/scan/watch-dirs/{id}", s.handleUpdateWatchDir)
	mux.HandleFunc("DELETE /api/scan/watch-dirs/{id}", s.handleDeleteWatchDir)
	mux.HandleFunc("GET /api/scan", s.handleLibraryScanStatus)
	mux.HandleFunc("POST /api/scan", s.handleLibraryScan)
	mux.HandleFunc("GET /api/session", s.handleSessionMe)
	mux.HandleFunc("GET /api/me", s.handleSessionMe)
	mux.HandleFunc("/api/movies", s.handleListMovies)
	mux.HandleFunc("GET /api/movies/{id}/tags", s.handleGetMovieTags)
	mux.HandleFunc("PUT /api/movies/{id}/tags", s.handleSetMovieTags)
	mux.HandleFunc("POST /api/movies/{id}/tags", s.handleSetMovieTags)
	mux.HandleFunc("GET /api/movies/{id}/titles", s.handleListMovieTitles)
	mux.HandleFunc("POST /api/movies/{id}/titles", s.handleAddMovieTitle)
	mux.HandleFunc("DELETE /api/movies/{id}/titles/{titleId}", s.handleDeleteMovieTitle)
	mux.HandleFunc("GET /api/movies/{id}/history", s.handleListMovieHistory)
	mux.HandleFunc("GET /api/movies/{id}/artwork", s.handleListMovieArtwork)
	mux.HandleFunc("POST /api/movies/{id}/artwork", s.handleReplaceMovieArtwork)
	mux.HandleFunc("GET /api/movies/{id}/subtitles", s.handleListMovieSubtitles)
	mux.HandleFunc("POST /api/movies/{id}/subtitles", s.handleUploadMovieSubtitles)
	mux.HandleFunc("GET /api/movies/{id}/files", s.handleListMovieFiles)
	mux.HandleFunc("DELETE /api/movies/{id}/files/{fileId}", s.handleDeleteMovieFileByID)
	mux.HandleFunc("/api/movies/", s.handleMovieByID)
	mux.HandleFunc("PATCH /api/movies/{id}", s.handlePatchMovie)
	mux.HandleFunc("DELETE /api/movies/{id}/file", s.handleDeleteMovieFile)
	mux.HandleFunc("DELETE /api/movies/{id}", s.handleDeleteMovie)
	mux.HandleFunc("POST /api/movies/{id}/refresh", s.handleRefreshMovie)
	mux.HandleFunc("PATCH /api/tv/seasons/{id}", s.handlePatchTVSeason)
	mux.HandleFunc("PATCH /api/tv/{id}", s.handlePatchTV)
	mux.HandleFunc("DELETE /api/tv/{id}", s.handleDeleteTV)
	mux.HandleFunc("POST /api/tv/{id}/refresh", s.handleRefreshTV)
	mux.HandleFunc("GET /api/tv/{id}/override", s.handleGetSeriesOverride)
	mux.HandleFunc("PUT /api/tv/{id}/override", s.handlePutSeriesOverride)
	mux.HandleFunc("POST /api/tv/{id}/override", s.handlePutSeriesOverride)
	mux.HandleFunc("DELETE /api/tv/{id}/override", s.handleDeleteSeriesOverride)
	mux.HandleFunc("PATCH /api/episodes/{id}", s.handlePatchEpisode)
	mux.HandleFunc("GET /api/episodes/{id}/file", s.handleGetEpisodeFile)
	mux.HandleFunc("DELETE /api/episodes/{id}/file", s.handleDeleteEpisodeFile)
	mux.HandleFunc("GET /api/collections", s.handleListCollections)
	mux.HandleFunc("GET /api/collections/{id}", s.handleCollectionByID)
	mux.HandleFunc("PATCH /api/collections/{id}", s.handleSetCollectionMonitored)
	mux.HandleFunc("PUT /api/collections/{id}", s.handleSetCollectionMonitored)
	mux.HandleFunc("POST /api/collections/{id}/sync", s.handleSyncCollection)
	mux.HandleFunc("GET /api/collections/", s.handleCollectionByID)
	mux.HandleFunc("/api/tv", s.handleListTV)
	mux.HandleFunc("GET /api/tv/{id}/tags", s.handleGetTVTags)
	mux.HandleFunc("PUT /api/tv/{id}/tags", s.handleSetTVTags)
	mux.HandleFunc("POST /api/tv/{id}/tags", s.handleSetTVTags)
	mux.HandleFunc("GET /api/tv/{id}/titles", s.handleListTVTitles)
	mux.HandleFunc("POST /api/tv/{id}/titles", s.handleAddTVTitle)
	mux.HandleFunc("DELETE /api/tv/{id}/titles/{titleId}", s.handleDeleteTVTitle)
	mux.HandleFunc("GET /api/tv/{id}/history", s.handleListTVHistory)
	mux.HandleFunc("GET /api/tv/{id}/artwork", s.handleListTVArtwork)
	mux.HandleFunc("POST /api/tv/{id}/artwork", s.handleReplaceTVArtwork)
	mux.HandleFunc("GET /api/tv/{id}/subtitles", s.handleListTVSubtitles)
	mux.HandleFunc("POST /api/tv/{id}/subtitles", s.handleUploadTVSubtitles)
	mux.HandleFunc("/api/tv/", s.handleTVByID)
	s.registerLibraryRoutes(mux)
	mux.HandleFunc("GET /api/jellyfin/status", s.handleJellyfinStatus)
	mux.HandleFunc("POST /api/jellyfin/sync", s.handleJellyfinSync)
	mux.HandleFunc("POST /api/jellyfin/refresh", s.handleJellyfinRefresh)
	mux.HandleFunc("GET /api/jellyfin/link", s.handleJellyfinLink)
	mux.HandleFunc("DELETE /api/jellyfin/link", s.handleDeleteJellyfinLink)
	mux.HandleFunc("POST /api/jellyfin/match", s.handleJellyfinMatch)
	mux.HandleFunc("/api/jellyfin/play", s.handleJellyfinPlay)
	mux.HandleFunc("GET /api/plex/sync-lists", s.handlePlexSyncLists)
	mux.HandleFunc("/api/plex/play", s.handlePlexPlay)
	mux.HandleFunc("GET /api/playback/resolve", s.handlePlaybackResolve)
	mux.HandleFunc("POST /api/playback/session", s.handlePlaybackSession)
	mux.HandleFunc("GET /api/sessions/events", s.handlePlaybackSessionEvents)
	mux.HandleFunc("GET /api/sessions", s.handlePlaybackSessions)
	mux.HandleFunc("POST /api/sessions/{id}/stop", s.handleStopPlaybackSession)
	mux.HandleFunc("GET /api/watch-stats/item", s.handleItemWatchStats)
	mux.HandleFunc("GET /api/watch-stats/stale", s.handleWatchStatsStale)
	mux.HandleFunc("GET /api/watch-stats/duplicates", s.handleWatchStatsDuplicates)
	mux.HandleFunc("GET /api/watch-stats/storage", s.handleWatchStatsStorage)
	mux.HandleFunc("GET /api/watch-stats/storage-history", s.handleWatchStatsStorageHistory)
	mux.HandleFunc("GET /api/watch-stats/charts", s.handleWatchStatsCharts)
	mux.HandleFunc("POST /api/watch-stats/import-tautulli", s.handleImportTautulli)
	mux.HandleFunc("POST /api/watch-stats/import-jellystat", s.handleImportJellystat)
	mux.HandleFunc("GET /api/watch-stats", s.handleWatchStats)
	mux.HandleFunc("GET /api/guard/rules", s.handleGetGuard)
	mux.HandleFunc("PUT /api/guard/rules", s.handleUpsertGuardRule)
	mux.HandleFunc("POST /api/guard/rules", s.handleUpsertGuardRule)
	mux.HandleFunc("DELETE /api/guard/rules/{id}", s.handleDeleteGuardRule)
	mux.HandleFunc("POST /api/guard/violations/ack", s.handleAckGuardViolations)
	mux.HandleFunc("POST /api/guard/trust/reset", s.handleResetGuardTrust)
	mux.HandleFunc("POST /api/guard/users/merge", s.handleMergeGuardUsers)
	mux.HandleFunc("GET /api/guard", s.handleGetGuard)
	mux.HandleFunc("GET /api/history", s.handlePlaybackHistory)
	mux.HandleFunc("GET /api/playback/subtitles", s.handlePlaybackSubtitlesList)
	mux.HandleFunc("GET /api/playback/subtitles/{id}", s.handlePlaybackSubtitleServe)
	mux.HandleFunc("GET /api/subtitles/search", s.handleSubtitleSearch)
	mux.HandleFunc("POST /api/subtitles/download", s.handleSubtitleDownload)
	mux.HandleFunc("GET /api/subtitles/blacklist", s.handleListSubtitleBlacklist)
	mux.HandleFunc("DELETE /api/subtitles/blacklist/{id}", s.handleRemoveSubtitleBlacklist)
	mux.HandleFunc("GET /api/subtitles/wanted", s.handleListSubtitleWanted)
	mux.HandleFunc("POST /api/subtitles/wanted", s.handleCreateSubtitleWanted)
	mux.HandleFunc("POST /api/subtitles/wanted/search", s.handleSearchSubtitleWanted)
	mux.HandleFunc("DELETE /api/subtitles/wanted/{id}", s.handleDeleteSubtitleWanted)
	mux.HandleFunc("GET /api/subtitles/providers", s.handleListSubtitleProviders)
	mux.HandleFunc("PUT /api/subtitles/providers/{id}", s.handleSetSubtitleProvider)
	mux.HandleFunc("POST /api/subtitles/providers/{id}", s.handleSetSubtitleProvider)
	mux.HandleFunc("GET /api/subtitles/history", s.handleListSubtitleHistory)
	mux.HandleFunc("POST /api/subtitles/history/clear", s.handleClearSubtitleHistory)
	mux.HandleFunc("GET /api/subtitles/profiles", s.handleListSubtitleProfiles)
	mux.HandleFunc("PUT /api/subtitles/profiles", s.handleUpsertSubtitleProfile)
	mux.HandleFunc("POST /api/subtitles/profiles", s.handleUpsertSubtitleProfile)
	mux.HandleFunc("GET /api/subtitles/languages", s.handleListSubtitleLanguages)
	mux.HandleFunc("GET /api/subtitles/media", s.handleListSubtitleMedia)
	mux.HandleFunc("POST /api/subtitles/media/mass-edit", s.handleMassEditSubtitleMedia)
	mux.HandleFunc("PATCH /api/subtitles/media/{id}", s.handlePatchSubtitleMedia)
	mux.HandleFunc("DELETE /api/subtitles/files/{id}", s.handleDeleteItemSubtitle)
	mux.HandleFunc("GET /api/tagging", s.handleGetTagging)
	mux.HandleFunc("POST /api/tagging/tags", s.handleCreateTaggingTag)
	mux.HandleFunc("DELETE /api/tagging/tags/{id}", s.handleDeleteTaggingTag)
	mux.HandleFunc("PUT /api/tagging/rules", s.handleUpsertTaggingRule)
	mux.HandleFunc("POST /api/tagging/rules", s.handleUpsertTaggingRule)
	mux.HandleFunc("DELETE /api/tagging/rules/{id}", s.handleDeleteTaggingRule)
	mux.HandleFunc("POST /api/tagging/classify", s.handleClassifyTagging)
	mux.HandleFunc("GET /api/tags", s.handleListTags)
	mux.HandleFunc("POST /api/tags", s.handleCreateTag)
	mux.HandleFunc("DELETE /api/tags/{id}", s.handleDeleteTag)
	mux.HandleFunc("GET /api/watch-notify", s.handleGetWatchNotify)
	mux.HandleFunc("PUT /api/watch-notify/rules", s.handleUpsertWatchNotifyRule)
	mux.HandleFunc("POST /api/watch-notify/rules", s.handleUpsertWatchNotifyRule)
	mux.HandleFunc("DELETE /api/watch-notify/rules/{id}", s.handleDeleteWatchNotifyRule)
	mux.HandleFunc("PUT /api/watch-notify/destinations", s.handleUpsertWatchNotifyDestination)
	mux.HandleFunc("POST /api/watch-notify/destinations", s.handleUpsertWatchNotifyDestination)
	mux.HandleFunc("DELETE /api/watch-notify/destinations/{id}", s.handleDeleteWatchNotifyDestination)
	mux.HandleFunc("POST /api/watch-notify/destinations/{id}/test", s.handleTestWatchNotifyDestination)
	mux.HandleFunc("GET /api/notifications", s.handleListNotifications)
	mux.HandleFunc("PUT /api/notifications", s.handleConfigureNotification)
	mux.HandleFunc("POST /api/notifications", s.handleConfigureNotification)
	mux.HandleFunc("POST /api/notifications/test", s.handleTestNotification)
	mux.HandleFunc("GET /api/maintainer", s.handleListMaintainer)
	mux.HandleFunc("POST /api/maintainer/scan", s.handleMaintainerScan)
	mux.HandleFunc("POST /api/maintainer/act", s.handleMaintainerAct)
	mux.HandleFunc("POST /api/maintainer/rules", s.handleUpsertMaintainerRule)
	mux.HandleFunc("POST /api/maintainer/rules/preview", s.handlePreviewMaintainerRule)
	mux.HandleFunc("GET /api/maintainer/rules/export", s.handleExportMaintainerRules)
	mux.HandleFunc("POST /api/maintainer/rules/import", s.handleImportMaintainerRules)
	mux.HandleFunc("POST /api/maintainer/rules/{id}/toggle", s.handleToggleMaintainerRule)
	mux.HandleFunc("DELETE /api/maintainer/rules/{id}", s.handleDeleteMaintainerRule)
	mux.HandleFunc("POST /api/maintainer/protections", s.handleUpsertMaintainerProtection)
	mux.HandleFunc("DELETE /api/maintainer/protections/{id}", s.handleDeleteMaintainerProtection)
	mux.HandleFunc("POST /api/maintainer/collections", s.handleUpsertMaintainerCollection)
	mux.HandleFunc("DELETE /api/maintainer/collections/{id}", s.handleDeleteMaintainerCollection)
	mux.HandleFunc("POST /api/maintainer/exclusions", s.handleUpsertMaintainerExclusion)
	mux.HandleFunc("POST /api/maintainer/exclusions/sync", s.handleSyncMaintainerExclusions)
	mux.HandleFunc("DELETE /api/maintainer/exclusions/{id}", s.handleDeleteMaintainerExclusion)
	mux.HandleFunc("POST /api/maintainer/candidates/{id}/{action}", s.handleMaintainerCandidateAction)
	mux.HandleFunc("GET /api/backups", s.handleListBackups)
	mux.HandleFunc("POST /api/backups", s.handleCreateBackup)
	mux.HandleFunc("DELETE /api/backups/{id}", s.handleDeleteBackup)
	mux.HandleFunc("POST /api/backups/{id}/restore", s.handleRestoreBackup)
	mux.HandleFunc("GET /api/playback/segments/media", s.handleListPlaybackSegmentMedia)
	mux.HandleFunc("GET /api/playback/segments", s.handlePlaybackSegments)
	mux.HandleFunc("PUT /api/playback/segments", s.handlePutPlaybackSegments)
	mux.HandleFunc("DELETE /api/playback/segments", s.handleDeletePlaybackSegments)
	mux.HandleFunc("GET /api/playback/chapters", s.handlePlaybackChapters)
	mux.HandleFunc("GET /api/playback/analysis", s.handlePlaybackAnalysis)
	mux.HandleFunc("GET /stream/transcode", s.handleTranscodeStream)
	mux.HandleFunc("GET /stream/hls", s.handleHLSIndex)
	mux.HandleFunc("GET /stream/hls/{key}/{file}", s.handleHLSAsset)
	mux.HandleFunc("GET /stream/trickplay", s.handleTrickplaySprite)
	mux.HandleFunc("GET /api/livetv", s.handleLiveTV)
	mux.HandleFunc("POST /api/livetv/timers", s.handleLiveTVTimer)
	mux.HandleFunc("/api/quickconnect", s.handleQuickConnect)
	mux.HandleFunc("/api/tv/login", s.handleTVLogin)
	mux.HandleFunc("/api/tv/login/totp", s.handleTVLoginTOTP)
	mux.HandleFunc("GET /api/mobile/auth/login", s.handleMobileAuthLogin)
	mux.HandleFunc("GET /api/mobile/auth/done", s.handleMobileAuthDone)
	mux.HandleFunc("POST /api/mobile/session", s.handleMobileSession)
	mux.HandleFunc("POST /api/password-reset/{id}/dismiss", s.handleDismissPasswordReset)
	mux.HandleFunc("POST /api/password-reset/{id}/password", s.handleSetPasswordReset)
	mux.HandleFunc("/api/password-reset", s.handlePasswordReset)
	mux.HandleFunc("GET /api/invite/peek", s.handleInvitePeek)
	mux.HandleFunc("POST /api/invite/redeem", s.handleInviteRedeem)
	mux.HandleFunc("GET /api/invites", s.handleListInvites)
	mux.HandleFunc("POST /api/invites", s.handleCreateInvite)
	mux.HandleFunc("DELETE /api/invites/{id}", s.handleRevokeInvite)
	mux.HandleFunc("GET /api/totp", s.handleGetTOTP)
	mux.HandleFunc("POST /api/totp", s.handleEnableTOTP)
	mux.HandleFunc("DELETE /api/totp", s.handleDisableTOTP)
	mux.HandleFunc("POST /api/totp/verify", s.handleVerifyTOTP)
	mux.HandleFunc("GET /api/passkeys", s.handleListPasskeys)
	mux.HandleFunc("POST /api/passkeys/register/begin", s.handleBeginPasskeyRegister)
	mux.HandleFunc("POST /api/passkeys/register/complete", s.handleCompletePasskeyRegister)
	mux.HandleFunc("DELETE /api/passkeys/{id}", s.handleDeletePasskey)
	mux.HandleFunc("GET /api/users", s.handleListUsers)
	mux.HandleFunc("POST /api/users", s.handleCreateUser)
	mux.HandleFunc("POST /api/users/{id}/password", s.handleSetUserPassword)
	mux.HandleFunc("PUT /api/users/{id}/password", s.handleSetUserPassword)
	mux.HandleFunc("PATCH /api/users/{id}", s.handlePatchUser)
	mux.HandleFunc("DELETE /api/users/{id}", s.handleDeleteUser)
	mux.HandleFunc("GET /api/keys", s.handleListAPIKeys)
	mux.HandleFunc("POST /api/keys", s.handleCreateAPIKey)
	mux.HandleFunc("POST /api/keys/{id}/rotate", s.handleRotateAPIKey)
	mux.HandleFunc("DELETE /api/keys/{id}", s.handleDeleteAPIKey)
	mux.HandleFunc("POST /api/debrid/add", s.handleDebridAdd)
	mux.HandleFunc("GET /api/debrid/vfs", s.handleDebridVFS)
	mux.HandleFunc("GET /api/debrid/stream", s.handleDebridStream)
	mux.HandleFunc("GET /api/music/tracks/", s.handleTrackLyrics)
	mux.HandleFunc("/api/userdata", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleUserdataGet(w, r)
		case http.MethodPut, http.MethodPost:
			s.handleUserdataPut(w, r)
		default:
			writeAPIMethodNotAllowed(w)
		}
	})
	s.registerRequestMediaRoutes(mux)
	mux.HandleFunc("GET /api/graph/related", s.handleGraphRelated)
	mux.HandleFunc("/api/discover/", s.handleDiscover)
	mux.HandleFunc("/api/watchlist", s.handleWatchlist)
	mux.HandleFunc("GET /api/lists/history", s.handleListSyncHistory)
	mux.HandleFunc("GET /api/lists/items", s.handleListSyncItems)
	mux.HandleFunc("GET /api/lists", s.handleListSources)
	mux.HandleFunc("POST /api/lists", s.handleCreateListSource)
	mux.HandleFunc("POST /api/lists/sync", s.handleSyncListSources)
	mux.HandleFunc("POST /api/lists/{id}/sync", s.handleSyncListSource)
	mux.HandleFunc("POST /api/lists/{id}/test", s.handleTestListSource)
	mux.HandleFunc("PATCH /api/lists/{id}", s.handleUpdateListSource)
	mux.HandleFunc("PUT /api/lists/{id}", s.handleUpdateListSource)
	mux.HandleFunc("DELETE /api/lists/{id}", s.handleDeleteListSource)
	mux.HandleFunc("POST /api/migrate", s.handleMigrate)
	// SPA uses /images/movies/<rel> and /images/tv/<rel>; modules serve under /images/<rel>.
	mux.Handle("/images/movies/", imagePrefixProxy("/images/movies", "/images", reverseProxy(s.moviesHTTP)))
	mux.Handle("/images/tv/", imagePrefixProxy("/images/tv", "/images", reverseProxy(s.tvHTTP)))
	mux.Handle("/images/music/", imagePrefixProxy("/images/music", "/images", reverseProxy(s.musicHTTP)))
	mux.Handle("/images/books/", imagePrefixProxy("/images/books", "/images", reverseProxy(s.booksHTTP)))
	mux.Handle("/images/audiobooks/", imagePrefixProxy("/images/audiobooks", "/images", reverseProxy(s.audiobooksHTTP)))
	mux.Handle("/images/comics/", imagePrefixProxy("/images/comics", "/images", reverseProxy(s.comicsHTTP)))
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
	userID    string
	username  string
	tenantID  string
	authToken string
	roles     []string
	expiry    time.Time
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, byID: make(map[string]sessionEntry)}
}

func (s *sessionStore) Create(userID, username string) (string, error) {
	return s.CreateWithRoles(userID, username, "", nil)
}

func (s *sessionStore) CreateWithTenant(userID, username, tenantID string) (string, error) {
	return s.CreateWithRoles(userID, username, tenantID, nil)
}

func (s *sessionStore) CreateWithRoles(userID, username, tenantID string, roles []string) (string, error) {
	return s.CreateWithAuth(userID, username, tenantID, roles, "")
}

func (s *sessionStore) CreateWithAuth(userID, username, tenantID string, roles []string, authToken string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b[:])
	s.mu.Lock()
	s.byID[tok] = sessionEntry{
		userID: userID, username: username, tenantID: strings.TrimSpace(tenantID),
		authToken: strings.TrimSpace(authToken),
		roles:     append([]string(nil), roles...),
		expiry:    time.Now().Add(s.ttl),
	}
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
	movies               mgmntv1.MovieManagementServiceClient
	moviesAdmin          mediaadminv1.MediaAdminServiceClient
	tv                   tvmgmtv1.TvManagementServiceClient
	tvAdmin              mediaadminv1.MediaAdminServiceClient
	music                musicv1.MusicManagementServiceClient
	musicAdmin           mediaadminv1.MediaAdminServiceClient
	booksAdmin           mediaadminv1.MediaAdminServiceClient
	arrHTTP              *http.Client
	jellyfin             jellyfinv1.JellyfinBridgeClient
	plex                 plexv1.PlexBridgeServiceClient
	notify               notifyv1.NotificationServiceClient
	backup               backupv1.BackupServiceClient
	maintainer           maintainv1.MaintainerServiceClient
	playbackGuard        guardv1.PlaybackGuardServiceClient
	backupRestoreDir     string
	indexer              indexerv1.IndexerServiceClient
	moviesHTTP           *url.URL
	tvHTTP               *url.URL
	requestHTTP          *url.URL
	musicHTTP            *url.URL
	booksHTTP            *url.URL
	comicsHTTP           *url.URL
	audiobooksHTTP       *url.URL
	transcoderHTTP       *url.URL
	debridHTTP           *url.URL
	acquisitionPeers     []acquisitionPeerDef
	graphHTTP            *url.URL
	graphToken           string
	playbackMonitorHTTP  *url.URL
	playbackMonitorToken string
	taggingHTTP          *url.URL
	subtitles            subtv1.SubtitleServiceClient
	subtitlesHTTP        *url.URL
	metadata             metadatav1.MetadataServiceClient
	listSync             listsyncv1.ListSyncServiceClient
	introOutro           introoutrov1.IntroOutroServiceClient
	ffprobe              ffprobev1.AnalysisServiceClient
	automation           automationv1.AutomationServiceClient
	scanner              scannerv1.ScannerServiceClient
	formats              formatsv1.FormatServiceClient
	roots                rootsv1.RootFolderServiceClient
	rename               renamev1.RenameServiceClient
	authHTTP             string // browser redirects
	authInternal         string // server-side code exchange
	publicURL            string // optional fixed public origin
	trustedProxies       []net.IPNet
	dist                 string
	requireAuth          bool
	sessions             *sessionStore
	userdata             *serverUserdata
	livetv               *liveTVStore
	libraryPaths         *libraryPathsStore
	quickconnect         *quickConnectStore
	passwordResets       *passwordResetStore
	issues               *mediaIssueStore
	together             *watchTogetherStore
}

func (s *server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/login", "/auth/callback", "/logout", "/api/quickconnect", "/api/tv/login", "/api/tv/login/totp",
			"/api/mobile/auth/login", "/api/mobile/auth/done", "/api/mobile/session",
			"/api/invite/peek", "/api/invite/redeem":
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/password-reset" && r.Method == http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/invite/") {
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
		if tok := bearerSessionToken(r); tok != "" && s.sessions.Valid(tok) {
			next.ServeHTTP(w, r)
			return
		}
		if wantsJSON(r) || strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/stream/") {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeAPIUnauthorized(w)
				return
			}
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

func bearerSessionToken(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if v := strings.TrimSpace(r.Header.Get("X-MuxCore-Session")); v != "" {
		return v
	}
	return ""
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
	sess, _, err := s.createSessionFromAuthCode(code)
	if err != nil {
		var ae *authExchangeError
		if errors.As(err, &ae) && ae.code == "auth.not_configured" {
			log.Printf("auth exchange: auth not configured (internal=%s)", s.authInternal)
		}
		writeAuthExchangeError(w, err)
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
	if r.TLS != nil {
		scheme = "https"
	} else if requestFromTrustedProxy(r, s.trustedProxies) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if requestFromTrustedProxy(r, s.trustedProxies) {
		if fwd := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); fwd != "" {
			host = fwd
		}
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
		writeAPIMethodNotAllowed(w)
		return
	}
	if lib := normalizeLibraryKey(r.URL.Query().Get("library")); lib != "" {
		s.handleLibraryMovies(w, r, lib)
		return
	}
	page, pageSize := pageParams(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.movies.ListMovies(ctx, &mgmntv1.ListMoviesRequest{Page: page, PageSize: pageSize})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "movies.gateway_error")
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
		writeAPIMethodNotAllowed(w)
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
		writeAPIError(w, http.StatusBadGateway, err.Error(), "movies.gateway_error")
		return
	}
	writeJSON(w, map[string]any{"movie": movieJSON(resp.GetMovie())})
}

func (s *server) handleListTV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
		return
	}
	page, pageSize := pageParams(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.tv.ListTVShows(ctx, &tvmgmtv1.ListTVShowsRequest{Page: page, PageSize: pageSize})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "tv.gateway_error")
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
		writeAPIMethodNotAllowed(w)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/tv/")
	id = strings.Trim(id, "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := s.tv.GetTVShow(ctx, &tvmgmtv1.GetTVShowRequest{SeriesId: id})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error(), "tv.gateway_error")
		return
	}
	show := tvJSON(resp.GetSeries())
	s.enrichTVEpisodeFiles(ctx, show)
	writeJSON(w, map[string]any{"show": show})
}

// handleJellyfinPlay resolves mux_id → Jellyfin item link → play deep-link URL.
// SPA: GET /api/jellyfin/play?mux_id=… → {"url":"…"}. 404 when unlinked; 503 when bridge down.
func (s *server) handleJellyfinPlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIMethodNotAllowed(w)
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
		"id":                 m.GetId(),
		"tmdb_id":            m.GetTmdbId(),
		"title":              m.GetTitle(),
		"year":               m.GetYear(),
		"overview":           m.GetOverview(),
		"runtime":            m.GetRuntime(),
		"vote_average":       m.GetVoteAverage(),
		"genres":             genres,
		"poster_url":         consumerImageURL("movies", firstNonEmpty(m.GetPosterUrl(), m.GetPosterPath())),
		"backdrop_url":       consumerImageURL("movies", firstNonEmpty(m.GetBackdropUrl(), m.GetBackdropPath())),
		"has_file":           m.GetHasFile(),
		"status":             m.GetStatus(),
		"tagline":            m.GetTagline(),
		"created_at":         m.GetCreatedAt(),
		"root_folder_path":   m.GetRootFolderPath(),
		"monitored":          m.GetMonitored(),
		"quality_profile_id": m.GetQualityProfileId(),
	}
	if m.GetHasFile() && m.GetId() != "" {
		out["stream_url"] = "/stream/movies/" + url.PathEscape(m.GetId())
	}
	if m.GetCollectionId() != 0 {
		out["collection_id"] = m.GetCollectionId()
		out["collection_name"] = m.GetCollectionName()
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
				if ep.GetId() != "" && ep.GetId() != "_list" {
					epStream = "/stream/tv/" + url.PathEscape(ep.GetId())
					if streamURL == "" {
						streamURL = epStream
					}
				}
			}
			if ep.GetId() == "_list" {
				continue
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
				"monitored":      ep.GetMonitored(),
			})
		}
		seasons = append(seasons, map[string]any{
			"id":            season.GetId(),
			"season_number": season.GetSeasonNumber(),
			"name":          season.GetName(),
			"episode_count": season.GetEpisodeCount(),
			"poster_url":    consumerImageURL("tv", season.GetPosterPath()),
			"episodes":      eps,
			"monitored":     season.GetMonitored(),
		})
	}
	return map[string]any{
		"id":                 m.GetId(),
		"tmdb_id":            m.GetTmdbId(),
		"title":              m.GetName(),
		"name":               m.GetName(),
		"year":               m.GetYear(),
		"overview":           m.GetOverview(),
		"vote_average":       m.GetVoteAverage(),
		"genres":             genres,
		"poster_url":         consumerImageURL("tv", firstNonEmpty(m.GetPosterUrl(), m.GetPosterPath())),
		"backdrop_url":       consumerImageURL("tv", firstNonEmpty(m.GetBackdropUrl(), m.GetBackdropPath())),
		"has_file":           hasFile,
		"stream_url":         streamURL,
		"status":             m.GetStatus(),
		"created_at":         m.GetCreatedAt(),
		"seasons":            seasons,
		"monitored":          m.GetMonitored(),
		"quality_profile_id": m.GetQualityProfileId(),
		"root_folder_path":   m.GetRootFolderPath(),
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
	if kind == "music" {
		return "/images/music/" + raw
	}
	if kind == "books" {
		return "/images/books/" + raw
	}
	if kind == "audiobooks" {
		return "/images/audiobooks/" + raw
	}
	if kind == "comics" {
		return "/images/comics/" + raw
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
	p.Rewrite = func(pr *httputil.ProxyRequest) {
		pr.SetURL(target)
		pr.Out.Host = target.Host
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

func optionalURL(raw string) *url.URL {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		log.Printf("optional url ignored %q: %v", raw, err)
		return nil
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
