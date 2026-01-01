package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"
)


type ctrlMsgKind string

const (
	ctrlPause ctrlMsgKind = "pause"
	ctrlStep  ctrlMsgKind = "step"
	ctrlSeed  ctrlMsgKind = "seed"
	ctrlReset ctrlMsgKind = "reset"
)

type ctrlMsg struct {
	kind   ctrlMsgKind
	paused bool
	n      int
	seed   int64
	players int
}

type Server struct {
	worldMu sync.RWMutex
	evMu    sync.RWMutex

	world *World
	tick  int

	events []Event
	subs   map[chan []byte]struct{}

	// dev controls
	ctrlCh      chan ctrlMsg
	paused      bool
	stepRemain  int
	playerCount int
}



func NewServer(w *World, playerCount int) *Server {
	return &Server{
		world:       w,
		playerCount: playerCount,
		events:      make([]Event, 0, 4096),
		subs:        map[chan []byte]struct{}{},
		ctrlCh:      make(chan ctrlMsg, 64),
	}
}

func (s *Server) appendEvent(e Event) {
	s.evMu.Lock()
	defer s.evMu.Unlock()
	s.events = append(s.events, e)
	if len(s.events) > 10000 {
		s.events = s.events[len(s.events)-6000:]
	}
}

func (s *Server) broadcast(payload any, eventName string) {
	b, _ := json.Marshal(payload)
	msg := []byte("event: " + eventName + "\ndata: " + string(b) + "\n\n")

	s.evMu.RLock()
	defer s.evMu.RUnlock()
	for ch := range s.subs {
		select {
		case ch <- msg:
		default:
			// drop if client is slow
		}
	}
}

func (s *Server) doTick(dt time.Duration) *WorldSnapshot {
	var snap *WorldSnapshot

	s.worldMu.Lock()
	s.tick++
	s.world.TickAllLocations()

	// forget sweep
	s.world.ForgetSweepIfNeeded()

	if s.tick%3 == 0 {
	  ws := BuildWorldSnapshot(s.world, s.tick, 0)
	  ws.Paused = s.paused
	  snap = &ws
	}
	s.worldMu.Unlock()

	return snap
}

func (s *Server) RunTicker(dt time.Duration) {
	t := time.NewTicker(dt)
	defer t.Stop()

	for {
		// если пауза и нет шагов — ждём команду
		if s.paused && s.stepRemain <= 0 {
			msg := <-s.ctrlCh
			s.applyCtrl(msg)
			continue
		}

		select {
		case msg := <-s.ctrlCh:
			s.applyCtrl(msg)

		case <-t.C:
			// если на паузе — тики по таймеру не исполняем
			if s.paused {
				continue
			}
			if snap := s.doTick(dt); snap != nil {
				s.broadcast(snap, "world")
			}

		default:
			// step-mode: выполняем шаги сразу, без ожидания таймера
			if s.paused && s.stepRemain > 0 {
				s.stepRemain--
				if snap := s.doTick(dt); snap != nil {
					s.broadcast(snap, "world")
				}
				continue
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
}

func (s *Server) applyCtrl(msg ctrlMsg) {
	switch msg.kind {
	case ctrlPause:
		s.paused = msg.paused

	case ctrlStep:
		if msg.n <= 0 {
			msg.n = 1
		}
		s.paused = true
		s.stepRemain += msg.n

	case ctrlSeed:
		s.worldMu.Lock()
		s.world.Rng = rand.New(rand.NewSource(msg.seed))
		s.world.Log(Event{
			T: time.Now(), K: "DEV_SEED",
			Msg: "rng reseeded",
			Ex: map[string]any{"seed": msg.seed},
		})
		s.worldMu.Unlock()

	case ctrlReset:
		pc := msg.players
		if pc <= 0 {
			pc = s.playerCount
		} else {
			s.playerCount = pc
		}

		// пересоздаём мир под локом
		s.worldMu.Lock()
		if s.world != nil && s.world.LogFile != nil {
			_ = s.world.LogFile.Close()
		}
		nw := NewWorld()
		seedWorld(nw, pc)
		// вернуть хук событий
		nw.OnEvent = s.appendEvent

		s.world = nw
		s.tick = 0
		s.stepRemain = 0
		s.worldMu.Unlock()

		// почистим in-mem events, чтобы не мешало
		s.evMu.Lock()
		s.events = s.events[:0]
		s.evMu.Unlock()
	}
}



func (s *Server) handleWorld(w http.ResponseWriter, r *http.Request) {
	z, err := strconv.Atoi(r.URL.Query().Get("z"))
	if err != nil {
		z = 0
	}
	if z != 0 && z != -1 {
		z = 0
	}

	s.worldMu.RLock()
	defer s.worldMu.RUnlock()

	snap := BuildWorldSnapshot(s.world, s.tick, z)
	snap.Paused = s.paused
	writeJSON(w, snap)
}


func (s *Server) handlePlayer(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	if id <= 0 {
		http.Error(w, "missing id", 400)
		return
	}
	s.worldMu.RLock()
	defer s.worldMu.RUnlock()
	p := s.world.FindPlayer(id)
	if p == nil {
		http.Error(w, "player not found", 404)
		return
	}
	writeJSON(w, BuildPlayerSnapshot(p))
}

func (s *Server) handleLoc(w http.ResponseWriter, r *http.Request) {
	x, _ := strconv.Atoi(r.URL.Query().Get("x"))
	y, _ := strconv.Atoi(r.URL.Query().Get("y"))
	z, _ := strconv.Atoi(r.URL.Query().Get("z"))

	key := LocKey{X: x, Y: y, Z: z}
	s.worldMu.RLock()
	defer s.worldMu.RUnlock()
	loc := s.world.GetOrCreateLocation(key)
	writeJSON(w, BuildLocationSnapshot(loc, s.world))
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 {
		tail = 200
	}
	s.evMu.RLock()
	defer s.evMu.RUnlock()
	ev := s.events
	if len(ev) > tail {
		ev = ev[len(ev)-tail:]
	}
	writeJSON(w, ev)
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", 500)
		return
	}

	ch := make(chan []byte, 32)
	s.evMu.Lock()
	s.subs[ch] = struct{}{}
	s.evMu.Unlock()

	defer func() {
		s.evMu.Lock()
		delete(s.subs, ch)
		s.evMu.Unlock()
		close(ch)
	}()

	// initial hello
	fmt.Fprintf(w, "event: hello\ndata: {\"ok\":true}\n\n")
	flusher.Flush()

	notify := r.Context().Done()

	for {
		select {
		case <-notify:
			return
		case msg := <-ch:
			_, _ = w.Write(msg)
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}


func (s *Server) handleDevPause(w http.ResponseWriter, r *http.Request) {
	// POST/GET: ?state=1|0  (если нет — toggle)
	q := r.URL.Query().Get("state")
	var paused bool
	if q == "" {
		paused = !s.paused
	} else {
		paused = (q == "1" || q == "true" || q == "on")
	}
	s.ctrlCh <- ctrlMsg{kind: ctrlPause, paused: paused}
	writeJSON(w, map[string]any{"ok": true, "paused": paused})
}

func (s *Server) handleDevStep(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 {
		n = 1
	}
	s.ctrlCh <- ctrlMsg{kind: ctrlStep, n: n}
	writeJSON(w, map[string]any{"ok": true, "step": n})
}

func (s *Server) handleDevSeed(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query().Get("value")
	if v == "" {
		http.Error(w, "missing value", 400)
		return
	}
	seed, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		http.Error(w, "bad value", 400)
		return
	}
	s.ctrlCh <- ctrlMsg{kind: ctrlSeed, seed: seed}
	writeJSON(w, map[string]any{"ok": true, "seed": seed})
}

func (s *Server) handleDevReset(w http.ResponseWriter, r *http.Request) {
	pc, _ := strconv.Atoi(r.URL.Query().Get("players"))
	s.ctrlCh <- ctrlMsg{kind: ctrlReset, players: pc}
	writeJSON(w, map[string]any{"ok": true, "players": pc})
}

