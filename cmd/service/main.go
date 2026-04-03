package main

import (
	"falcon/server"
)

func main() {
	srv := server.NewServer()
	srv.Run()
}
