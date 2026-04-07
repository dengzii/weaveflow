package main

import (
	"weaveflow/internal/server"
)

func main() {
	srv := server.NewServer()
	srv.Run()
}
