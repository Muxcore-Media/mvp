package main

import (
	"bufio"
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
