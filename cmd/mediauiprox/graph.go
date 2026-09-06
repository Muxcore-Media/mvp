package main

import (
	"net/http"

	"github.com/Muxcore-Media/mvp-smoke/cmd/mediauiprox/graphrelated"
)

func (s *server) handleGraphRelated(w http.ResponseWriter, r *http.Request) {
	graphrelated.Handle(s.graphHTTP, s.graphToken)(w, r)
}
