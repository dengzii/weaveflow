package main

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static/*
var staticFiles embed.FS

type appServer struct {
	neoAddr       string
	indexTemplate *template.Template
	staticHandler http.Handler
}

type indexViewData struct {
	NeoAddr string
}

func newAppServer(neoAddr string) (*appServer, error) {
	indexTemplate, err := template.ParseFS(staticFiles, "static/index.html")
	if err != nil {
		return nil, err
	}

	staticDir, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}

	return &appServer{
		neoAddr:       strings.TrimRight(strings.TrimSpace(neoAddr), "/"),
		indexTemplate: indexTemplate,
		staticHandler: http.StripPrefix("/static/", http.FileServer(http.FS(staticDir))),
	}, nil
}

func (s *appServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", s.staticHandler)
	mux.HandleFunc("/", s.handleIndex)
	return mux
}

func (s *appServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.indexTemplate.Execute(w, indexViewData{
		NeoAddr: s.neoAddr,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
