package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/aditya/xenia/node/internal/service"
)

//go:embed www/*
var wwwFS embed.FS

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	nodeID := flag.String("node-id", "node1", "stable identifier for this node")
	storageDir := flag.String("storage-dir", "", "directory for offline message persistence")
	flag.Parse()

	// Extract the embedded static files
	staticFiles, err := fs.Sub(wwwFS, "www")
	if err != nil {
		log.Fatalf("failed to load static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticFiles))

	server := service.New(*nodeID, service.WithListenAddr(*listen), service.WithStorageDir(*storageDir))
	server.StartUDPDiscovery()
	apiHandler := server.Handler()

	// Create a master router that routes to API or Static Files
	masterMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Route explicit API endpoints to the backend service
		if strings.HasPrefix(r.URL.Path, "/health") ||
			strings.HasPrefix(r.URL.Path, "/users") ||
			strings.HasPrefix(r.URL.Path, "/presence") ||
			strings.HasPrefix(r.URL.Path, "/message") ||
			strings.HasPrefix(r.URL.Path, "/calls") ||
			strings.HasPrefix(r.URL.Path, "/internal") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		// Fallback everything else to the embedded React frontend
		// This ensures /index.html, /favicon.svg, and PWA manifests work flawlessly!
		fileServer.ServeHTTP(w, r)
	})

	log.Printf("Xenia node %q listening on http://%s", *nodeID, *listen)
	log.Fatal(http.ListenAndServe(*listen, corsMiddleware(masterMux)))
}
