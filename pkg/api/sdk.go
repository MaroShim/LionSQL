package api

import (
	_ "embed"
	"net/http"
)

//go:embed sdk/lionsql.js
var lionsqlJSSource []byte

//go:embed sdk/lionsql.d.ts
var lionsqlDTSSource []byte

func (s *Server) handleServeSDKJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(lionsqlJSSource)
}

func (s *Server) handleServeSDKDTS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/typescript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(lionsqlDTSSource)
}
