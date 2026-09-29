package api

import (
	_ "embed"
	"net/http"
)

//go:embed sdk/sqlion.js
var sqlionJSSource []byte

//go:embed sdk/sqlion.d.ts
var sqlionDTSSource []byte

func (s *Server) handleServeSDKJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(sqlionJSSource)
}

func (s *Server) handleServeSDKDTS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/typescript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(sqlionDTSSource)
}
