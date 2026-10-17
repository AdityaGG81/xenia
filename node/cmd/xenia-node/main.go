package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"flag"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aditya/xenia/node/internal/service"
)

//go:embed www/*
var wwwFS embed.FS

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	nodeID := flag.String("node-id", "node1", "stable identifier for this node")
	storageDir := flag.String("storage-dir", "", "directory for offline message persistence (default: user config directory)")
	advertiseURL := flag.String("advertise-url", "", "mesh-reachable HTTP URL announced to peers")
	discoveryIface := flag.String("discovery-iface", "", "interface used for UDP peer broadcasts (default: all IPv4 interfaces)")
	meshToken := flag.String("mesh-token", os.Getenv("XENIA_MESH_TOKEN"), "shared token required by internal node APIs (or XENIA_MESH_TOKEN)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (enables HTTPS when set with --tls-key)")
	tlsKey := flag.String("tls-key", "", "TLS private key file")
	peerCA := flag.String("peer-ca", "", "CA PEM file used to trust HTTPS certificates from mesh peers")
	flag.Parse()
	if !validNodeID(*nodeID) {
		log.Fatal("node-id must be 1–64 letters, numbers, dots, dashes, or underscores, and cannot be . or ..")
	}
	if *advertiseURL != "" && !validAdvertiseURL(*advertiseURL) {
		log.Fatal("advertise-url must be an HTTP URL with a private or link-local IP address and no path")
	}
	if _, _, err := net.SplitHostPort(*listen); err != nil {
		log.Fatalf("invalid listen address: %v", err)
	}
	if (*tlsCert == "") != (*tlsKey == "") {
		log.Fatal("both --tls-cert and --tls-key are required for HTTPS")
	}
	var peerTLSConfig *tls.Config
	if *peerCA != "" {
		caPEM, err := os.ReadFile(*peerCA)
		if err != nil {
			log.Fatalf("read peer CA: %v", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caPEM) {
			log.Fatal("peer CA file contains no usable certificates")
		}
		peerTLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	if *storageDir == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			log.Fatalf("resolve user config directory: %v", err)
		}
		*storageDir = filepath.Join(configDir, "xenia", *nodeID)
	}

	// Extract the embedded static files
	staticFiles, err := fs.Sub(wwwFS, "www")
	if err != nil {
		log.Fatalf("failed to load static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticFiles))

	server := service.New(*nodeID, service.WithListenAddr(*listen), service.WithAdvertiseURL(*advertiseURL), service.WithDiscoveryInterface(*discoveryIface), service.WithStorageDir(*storageDir), service.WithMeshToken(*meshToken), service.WithPeerTLSConfig(peerTLSConfig))
	server.StartUDPDiscovery()
	apiHandler := server.Handler()

	// Create a master router that routes to API or Static Files
	masterMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Route explicit API endpoints to the backend service
		if r.URL.Path == "/health" || r.URL.Path == "/register" || r.URL.Path == "/users" || r.URL.Path == "/ws" ||
			strings.HasPrefix(r.URL.Path, "/internal/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		// Fallback everything else to the embedded React frontend
		// This ensures /index.html, /favicon.svg, and PWA manifests work flawlessly!
		fileServer.ServeHTTP(w, r)
	})

	handler := masterMux
	if *tlsCert != "" || *tlsKey != "" {
		if err := serve(*nodeID, *listen, handler, *tlsCert, *tlsKey, server); err != nil {
			log.Fatal(err)
		}
		return
	}
	log.Printf("Xenia node %q serving HTTP on %s", *nodeID, *listen)
	if err := serve(*nodeID, *listen, handler, "", "", server); err != nil {
		log.Fatal(err)
	}
}

func serve(nodeID, address string, handler http.Handler, certFile, keyFile string, node *service.Server) error {
	defer func() {
		if err := node.Close(); err != nil {
			log.Printf("node shutdown failed: %v", err)
		}
	}()
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	serveErr := make(chan error, 1)
	go func() {
		if certFile != "" {
			log.Printf("Xenia node %q serving HTTPS on %s", nodeID, address)
			serveErr <- server.ListenAndServeTLS(certFile, keyFile)
			return
		}
		serveErr <- server.ListenAndServe()
	}()

	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
	case <-stop.Done():
		ctx, shutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdown()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
			_ = server.Close()
		}
	}
	return nil
}

func validAdvertiseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || !(ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return false
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return false
		}
	}
	return true
}

func validNodeID(id string) bool {
	if id == "" || len(id) > 64 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '.' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}
