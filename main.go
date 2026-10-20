// Berth deploys Linux programs to servers over SSH and runs them with the
// servers' own systemd.
package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"

	"github.com/xusenlin/berth/core/config"
	"github.com/xusenlin/berth/services"
)

func main() {
	app := mygo.App
	app.SetName("Berth")

	path := os.Getenv("BERTH_CONFIG")
	if path == "" {
		var err error
		if path, err = config.DefaultPath(); err != nil {
			log.Fatal(err)
		}
	}
	store, err := config.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	var secrets config.Secrets = config.Keyring{}
	if debugSecrets != nil {
		secrets = debugSecrets()
	}
	services.ApplyTheme(store.Settings().Theme)
	b := services.NewBackend(store, secrets)
	agents := services.NewMCPServer(b)
	mygo.Bind(&services.Servers{B: b}, &services.Apps{B: b}, &services.Settings{B: b}, &services.MCP{S: agents})
	agents.Start()

	app.WhenReady(func() {
		app.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleViewMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		openWindow()
	})
	app.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			openWindow()
		}
	})
	app.OnQuit(func() {
		agents.Close()
		b.Pool.CloseAll()
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// Set by debug.go in builds tagged berthdebug.
var (
	debugWindow  func(*mygo.Window)
	debugSecrets func() config.Secrets
)

func openWindow() {
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:          "Berth",
		Width:          1180,
		Height:         760,
		MinWidth:       900,
		MinHeight:      560,
		TitleBarStyle:  mygo.TitleBarHidden,
		TitleBarHeight: 44, // the page's header, h-11
		// Centers the 14 px traffic lights in that header on macOS.
		TrafficLightPosition: &mygo.Point{X: 16, Y: 15},
		BackgroundColor:      "light-dark(#ffffff, #0a0a0a)",
		StateKey:             "main",
		URL:                  "/",
	})
	if debugWindow != nil {
		debugWindow(win)
	}
}
