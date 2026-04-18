package main

import (
	"fmt"
	"log"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/hdadhich01/spout/internal/server"
	"github.com/hdadhich01/spout/internal/store"
)

func main() {
	dir := config.DefaultStorageDir()
	st, err := store.New(dir)
	if err != nil {
		log.Fatalf("opening store: %v", err)
	}

	port := config.DefaultLocalPort
	app := server.New(st)
	fmt.Printf("spout server listening on http://%s\n", config.DefaultLocalAddr())
	fmt.Printf("storage at %s\n", dir)
	log.Fatal(app.Listen(":" + port))
}
