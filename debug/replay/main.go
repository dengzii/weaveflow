package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
)

func main() {
	addr := flag.String("addr", ":8091", "listen address")
	cacheDir := flag.String("cache-dir", ".local/instance", "default graph cache directory")
	flag.Parse()

	server, err := newAppServer(*cacheDir)
	if err != nil {
		log.Fatal(err)
	}

	displayURL := *addr
	if strings.HasPrefix(displayURL, ":") {
		displayURL = "http://127.0.0.1" + displayURL
	} else if !strings.Contains(displayURL, "://") {
		displayURL = "http://" + displayURL
	}

	fmt.Printf("graph debug viewer listening on %s\n", displayURL)
	fmt.Printf("default cache dir: %s\n", *cacheDir)

	if err := http.ListenAndServe(*addr, server.routes()); err != nil {
		log.Fatal(err)
	}
}
