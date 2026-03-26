package main

import (
	"falcon"
	"falcon/server"
)

func main() {
	srv := server.NewServer()
	srv.Run(falcon.NewModelManager())
}
