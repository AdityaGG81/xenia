// xenia-access-proxy exposes one virtual-node HTTP service to a physical lab network.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func main() {
	listen := flag.String("listen", "10.42.0.1:8081", "physical-network listen address")
	upstream := flag.String("upstream", "http://172.20.1.2:8080", "virtual node HTTP endpoint")
	flag.Parse()

	target, err := url.Parse(*upstream)
	if err != nil {
		log.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy %s: %v", r.URL.Path, err)
		http.Error(w, "virtual node unavailable", http.StatusBadGateway)
	}
	log.Printf("Xenia access proxy listening on http://%s -> %s", *listen, target)
	log.Fatal(http.ListenAndServe(*listen, proxy))
}
