package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// recordingMux wraps a real ServeMux (so duplicate/conflicting patterns still
// panic exactly as in production) and records every registered pattern.
type recordingMux struct {
	mux      *http.ServeMux
	patterns []string
}

func (m *recordingMux) Handle(pattern string, h http.Handler) {
	m.patterns = append(m.patterns, pattern)
	m.mux.Handle(pattern, h)
}

func (m *recordingMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.mux.HandleFunc(pattern, h)
}

// normalizeRoute turns a ServeMux pattern into the "METHOD /path" form used in
// BFF-API.md. Patterns registered without a method accept any method and are
// documented as ANY.
func normalizeRoute(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if i := strings.IndexByte(pattern, ' '); i > 0 && !strings.HasPrefix(pattern, "/") {
		return strings.ToUpper(pattern[:i]) + " " + strings.TrimSpace(pattern[i+1:])
	}
	return "ANY " + pattern
}

func registeredRoutes(t *testing.T) []string {
	t.Helper()
	// Zero-value server is enough: registration only binds method values and
	// reads upstream URLs lazily inside handlers or at proxy construction.
	s := &server{}
	rec := &recordingMux{mux: http.NewServeMux()}
	s.registerRoutes(rec)
	set := map[string]bool{}
	for _, p := range rec.patterns {
		set[normalizeRoute(p)] = true
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

var inventoryRow = regexp.MustCompile("^\\|\\s*`([A-Z]+ /[^`]*)`\\s*\\|\\s*(\\S.*?)\\s*\\|\\s*$")

// documentedRoutes parses the "## Route inventory" section of BFF-API.md.
// Each row is: | `METHOD /pattern` | one-line purpose |
func documentedRoutes(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "BFF-API.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	docs := map[string]string{}
	in := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "Route inventory"
			continue
		}
		if !in {
			continue
		}
		if m := inventoryRow.FindStringSubmatch(line); m != nil {
			if _, dup := docs[m[1]]; dup {
				t.Errorf("BFF-API.md documents %q more than once", m[1])
			}
			docs[m[1]] = m[2]
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return docs
}

// TestRouteInventoryMatchesBFFAPIDoc fails when a registered route is missing
// from BFF-API.md "Route inventory" or a documented route is not registered
// (SDD §5 / TDD §7, roadmap T-M2-08).
func TestRouteInventoryMatchesBFFAPIDoc(t *testing.T) {
	registered := registeredRoutes(t)
	docs := documentedRoutes(t)
	if len(docs) == 0 {
		t.Fatal(`BFF-API.md has no "## Route inventory" table rows`)
	}
	reg := map[string]bool{}
	for _, r := range registered {
		reg[r] = true
		if _, ok := docs[r]; !ok {
			t.Errorf("registered route not documented in BFF-API.md Route inventory: %s", r)
		}
	}
	for d, purpose := range docs {
		if !reg[d] {
			t.Errorf("documented route is not registered: %s", d)
		}
		if strings.TrimSpace(purpose) == "" {
			t.Errorf("documented route has empty purpose: %s", d)
		}
	}
}

// registeredPatterns returns every pattern exactly as registerRoutes passes it
// to the mux (the parental route-class table is keyed by these strings). A
// pattern without a class makes registerRoutes panic; that is reported as a
// test failure naming the pattern.
func registeredPatterns(t *testing.T) []string {
	t.Helper()
	rec := &recordingMux{mux: http.NewServeMux()}
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("registerRoutes: %v", p)
			}
		}()
		(&server{}).registerRoutes(rec)
	}()
	return rec.patterns
}

// TestParentalRouteClassesCoverEveryRoute is the ADR-0031 inventory test
// (required negative test 1): every registered pattern has exactly one route
// class and the table names no pattern that is not registered.
func TestParentalRouteClassesCoverEveryRoute(t *testing.T) {
	patterns := registeredPatterns(t)
	if len(patterns) < 300 {
		t.Fatalf("only %d patterns recorded; registration did not run", len(patterns))
	}
	registered := map[string]bool{}
	for _, p := range patterns {
		registered[p] = true
		route, ok := parentalRouteClasses[p]
		if !ok {
			t.Errorf("registered pattern has no parental route class: %q", p)
			continue
		}
		switch route.class {
		case classList, classItem, classDeny, classExempt:
		case classPlay:
			if route.item == nil && !route.hlsAsset {
				t.Errorf("C-PLAY pattern %q cannot locate its item", p)
			}
		default:
			t.Errorf("pattern %q has unknown class %q", p, route.class)
		}
	}
	for p := range parentalRouteClasses {
		if !registered[p] {
			t.Errorf("parental route class for unregistered pattern: %q", p)
		}
	}
}

// TestParentalRegistrarRejectsUnclassifiedPattern proves the startup guard
// fires: a new route without a class cannot be registered.
func TestParentalRegistrarRejectsUnclassifiedPattern(t *testing.T) {
	reg := parentalRegistrar{s: &server{}, next: http.NewServeMux()}
	defer func() {
		if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), "GET /api/unclassified") {
			t.Fatalf("expected panic naming the pattern, got %v", p)
		}
	}()
	reg.HandleFunc("GET /api/unclassified", func(http.ResponseWriter, *http.Request) {})
}

// TestParentalGateOnlyThroughRegisterRoutes keeps the ungated route list
// (registerClassifiedRoutes) reachable from production code only via
// registerRoutes.
func TestParentalGateOnlyThroughRegisterRoutes(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f) //nolint:gosec // package source files
		if err != nil {
			t.Fatal(err)
		}
		calls += strings.Count(string(raw), "registerClassifiedRoutes(")
	}
	// The definition plus the single call inside registerRoutes.
	if calls != 2 {
		t.Fatalf("registerClassifiedRoutes( appears %d times in production code, want 2", calls)
	}
}

var parentalDocRow = regexp.MustCompile("^\\|\\s*`([A-Z]+ /[^`]*)`\\s*\\|\\s*(C-[A-Z]+)\\s*\\|")

// TestParentalRouteClassesMatchBFFAPIDoc keeps the BFF-API.md "Parental route
// classes" table equal to the non-exempt rows of parentalRouteClasses.
func TestParentalRouteClassesMatchBFFAPIDoc(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "BFF-API.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	docs := map[string]string{}
	in := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "Parental route classes"
			continue
		}
		if m := parentalDocRow.FindStringSubmatch(line); in && m != nil {
			if _, dup := docs[m[1]]; dup {
				t.Errorf("BFF-API.md documents %q twice", m[1])
			}
			docs[m[1]] = m[2]
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(docs) == 0 {
		t.Fatal(`BFF-API.md has no "## Parental route classes" rows`)
	}
	want := map[string]string{}
	for p, route := range parentalRouteClasses {
		if route.class != classExempt {
			want[normalizeRoute(p)] = string(route.class)
		}
	}
	for r, class := range want {
		if docs[r] != class {
			t.Errorf("BFF-API.md class for %s = %q, want %s", r, docs[r], class)
		}
	}
	for r := range docs {
		if _, ok := want[r]; !ok {
			t.Errorf("BFF-API.md lists %s, which is C-EXEMPT or unregistered", r)
		}
	}
}
