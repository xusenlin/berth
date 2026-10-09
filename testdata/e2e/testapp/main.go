// testapp is the program Berth's end-to-end tests deploy: an HTTP server
// that answers /health, or one that exits at once when built broken.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

// version and broken are set with -ldflags -X.
var (
	version = "dev"
	broken  = "false"
)

func main() {
	addr := flag.String("http", "127.0.0.1:8080", "listen address")
	data := flag.String("data", "", "a file to append starts to")
	flag.Parse()
	fmt.Println("\x1b[32mstarting\x1b[0m testapp", version, "on", *addr, "GREETING="+os.Getenv("GREETING"))
	if broken == "true" {
		fmt.Fprintln(os.Stderr, "broken build, exiting")
		os.Exit(1)
	}
	if *data != "" {
		f, err := os.OpenFile(*data, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintln(f, version, time.Now().Format(time.RFC3339))
			f.Close()
		}
	}
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok", version) })
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "testapp", version) })
	go func() {
		for range time.Tick(time.Second) {
			fmt.Println("tick", version)
		}
	}()
	if err := http.ListenAndServe(*addr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
