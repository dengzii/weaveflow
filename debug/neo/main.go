package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
)

func main() {
	addr := flag.String("addr", ":8092", "listen address")
	neoAddr := flag.String("neo-addr", "http://localhost:9090", "neo API server address")
	flag.Parse()

	server, err := newAppServer(*neoAddr)
	if err != nil {
		log.Fatal(err)
	}

	displayURL := *addr
	if strings.HasPrefix(displayURL, ":") {
		displayURL = "http://127.0.0.1" + displayURL
	} else if !strings.Contains(displayURL, "://") {
		displayURL = "http://" + displayURL
	}

	fmt.Printf("neo web client listening on %s\n", displayURL)
	fmt.Printf("neo API server: %s\n", *neoAddr)

	if err := http.ListenAndServe(*addr, server.routes()); err != nil {
		log.Fatal(err)
	}
}
