//go:build berthdebug

// The debug build lets tools drive the window: POST JavaScript to /eval,
// GET a PNG of the page from /capture, POST a path to /pick to answer the
// next file dialog. It listens on BERTH_DEBUG_ADDR
// (127.0.0.1:7788 by default) and never ships: build with -tags berthdebug.
//
//	MYGO_DEV_URL=http://localhost:5173 go run -tags berthdebug .
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/egoist/mygo"

	"github.com/xusenlin/berth/core/config"
	"github.com/xusenlin/berth/services"
)

func init() {
	var once sync.Once
	var current *mygo.Window
	var mu sync.Mutex
	debugWindow = func(w *mygo.Window) {
		mu.Lock()
		current = w
		mu.Unlock()
		once.Do(func() {
			// go run has no app bundle: show the icon in the Dock anyway.
			if png, err := os.ReadFile("design/icon.png"); err == nil {
				_ = mygo.App.Dock.SetIcon(png)
			}
			addr := os.Getenv("BERTH_DEBUG_ADDR")
			if addr == "" {
				addr = "127.0.0.1:7788"
			}
			win := func() *mygo.Window { mu.Lock(); defer mu.Unlock(); return current }
			mux := http.NewServeMux()
			mux.HandleFunc("POST /eval", func(rw http.ResponseWriter, r *http.Request) {
				code, _ := io.ReadAll(r.Body)
				v, err := win().Page().EvalContext(r.Context(), string(code))
				if err != nil {
					http.Error(rw, err.Error(), http.StatusInternalServerError)
					return
				}
				json.NewEncoder(rw).Encode(v)
			})
			var pick string
			services.DebugPick = func() string { mu.Lock(); defer mu.Unlock(); return pick }
			mux.HandleFunc("POST /pick", func(rw http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				pick = string(b)
				mu.Unlock()
			})
			mux.HandleFunc("GET /capture", func(rw http.ResponseWriter, r *http.Request) {
				png, err := win().CapturePage()
				if err != nil {
					http.Error(rw, err.Error(), http.StatusInternalServerError)
					return
				}
				rw.Header().Set("Content-Type", "image/png")
				rw.Write(png)
			})
			go func() { log.Println("debug:", http.ListenAndServe(addr, mux)) }()
		})
	}
	if os.Getenv("BERTH_MEMORY_SECRETS") == "1" {
		debugSecrets = func() config.Secrets { return config.MemorySecrets{} }
	}
}
