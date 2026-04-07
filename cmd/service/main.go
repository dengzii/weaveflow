package main

import (
	"falcon/internal/server"
)

func main() {
	srv := server.NewServer()
	srv.Run()
}
