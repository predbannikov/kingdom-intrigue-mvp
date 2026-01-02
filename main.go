package main

import (
	"flag"
	"fmt"
	"net/http"
	"time"
)

func main() {
	port := flag.Int("port", 8080, "http port")
	players := flag.Int("players", 500, "players")
	tickms := flag.Int("tickms", 80, "tick interval (ms)")
	flag.Parse()

	w := NewWorld()
	seedWorld(w, *players)

	srv := NewServer(w, *players)

	// Hook events to in-memory ring buffer too.
	w.OnEvent = srv.appendEvent

	// Run tick loop
	go srv.RunTicker(time.Duration(*tickms) * time.Millisecond)

	mux := http.NewServeMux()

	// API
	mux.HandleFunc("/api/world", srv.handleWorld)
	mux.HandleFunc("/api/player", srv.handlePlayer)
	mux.HandleFunc("/api/loc", srv.handleLoc)
	mux.HandleFunc("/api/events", srv.handleEvents)
	mux.HandleFunc("/api/stream", srv.handleStream)

	mux.HandleFunc("/api/dev/pause", srv.handleDevPause)
	mux.HandleFunc("/api/dev/step", srv.handleDevStep)
	mux.HandleFunc("/api/dev/seed", srv.handleDevSeed)
	mux.HandleFunc("/api/dev/reset", srv.handleDevReset)

	// Market (POI v1)
	mux.HandleFunc("/api/market/poi", srv.handleMarketPOI)
	mux.HandleFunc("/api/market/poi/create", srv.handleMarketPOICreate)
	mux.HandleFunc("/api/market/poi/buy", srv.handleMarketPOIBuy)
	mux.HandleFunc("/api/dev/market/sell_pinned_poi", srv.handleDevSellPinnedPOI)

	mux.HandleFunc("/api/debug/world", srv.handleDebugWorld)

	// Static UI
	fs := http.FileServer(http.Dir("./web"))
	mux.Handle("/", fs)

	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("UI:  http://localhost:%d/\n", *port)
	fmt.Printf("API: http://localhost:%d/api/world\n", *port)
	_ = http.ListenAndServe(addr, mux)
}
