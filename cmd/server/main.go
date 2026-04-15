package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/hdadhich01/spout/internal/server"
	"github.com/hdadhich01/spout/internal/store"
)

func main() {
	dir := filepath.Join(configDir(), "sessions")
	st, err := store.New(dir)
	if err != nil {
		log.Fatalf("opening store: %v", err)
	}

	port := config.DefaultLocalPort
	app := server.New(st)
	fmt.Printf("spout server listening on http://%s\n", config.DefaultLocalAddr())
	fmt.Printf("sessions stored in %s\n", dir)
	log.Fatal(app.Listen(":" + port))
}

func configDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "spout")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "spout")
}
