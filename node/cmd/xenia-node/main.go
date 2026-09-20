package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/aditya/xenia/node/internal/service"
)

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
	peerURL := flag.String("peer-url", "", "HTTP URL of this node's signalling peer")
	storageDir := flag.String("storage-dir", "", "directory for offline message persistence")
	flag.Parse()

	server := service.New(*nodeID, service.WithPeerURL(*peerURL), service.WithStorageDir(*storageDir))
	log.Printf("Xenia node %q listening on http://%s", *nodeID, *listen)
	log.Fatal(http.ListenAndServe(*listen, corsMiddleware(server.Handler())))
}
