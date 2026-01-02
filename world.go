package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"time"
)

func seedWorld(w *World, playerCount int) {
	start := LocKey{X: StartWorldX, Y: StartWorldY, Z: 0}
	loc := w.GetOrCreateLocation(start)
	now := time.Now()

	for i := 1; i <= playerCount; i++ {
		p := &Player{
			ID:             i,
			Loc:            loc.Key,
			X:              w.Rng.Intn(LocSize),
			Y:              w.Rng.Intn(LocSize),
			Alive:          true,
			HP:             BaseHP,
			Ink:            w.Rng.Intn(3),
			Gold:           2 + w.Rng.Intn(10),
			KnownLocations: map[LocKey]LocKnowLevel{},
			KnownPOI:       map[string]POIKnow{},
			LastPOILoot:    map[string]time.Time{},
			SpawnProtect:   now.Add(SpawnInvuln),
			SeenPOI:        map[string]time.Time{},
			SeenPOICount:   map[string]int{},
		}
		loc.Players[p.ID] = p
		p.KnownLocations[loc.Key] = LocKnowExperienced
	}
	w.RebuildOcc(loc)
	w.Log(Event{T: time.Now(), K: "BOOT", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, Msg: "world started", Ex: map[string]any{"players": len(loc.Players)}})
}

func NewWorld() *World {
	f, err := os.Create("events.jsonl")
	if err != nil {
		panic(err)
	}
	return &World{
		Locations:      map[LocKey]*Location{},
		Battles:        map[int]*Battle{},
		MarketPOI:      map[int64]*POIListing{},
		NextPOIListing: 1,
		NextBattleID:   1,
		NextEntranceID: 1,
		NextPocketID:   1,
		LogFile:        f,
		Rng:            rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (w *World) FindPlayer(id int) *Player {
	for _, loc := range w.Locations {
		if p := loc.Players[id]; p != nil {
			return p
		}
	}
	return nil
}

// ------------------- WORLD LOGIC -------------------

func (w *World) TickAllLocations() {
	keys := make([]LocKey, 0, len(w.Locations)+4)
	for k := range w.Locations {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Z != keys[j].Z {
			return keys[i].Z < keys[j].Z
		}
		if keys[i].Y != keys[j].Y {
			return keys[i].Y < keys[j].Y
		}
		return keys[i].X < keys[j].X
	})
	for _, k := range keys {
		loc := w.GetOrCreateLocation(k)
		if len(loc.Players) == 0 {
			continue
		}
		w.TickLocation(k)
	}
}

func (w *World) TickLocation(key LocKey) {
	loc := w.GetOrCreateLocation(key)
	now := time.Now()

	w.UpdateBattles(loc, now)

	for _, p := range loc.Players {
		if !p.Alive {
			// respawn after delay
			if !p.DeadUntil.IsZero() && now.After(p.DeadUntil) {
				w.RespawnPlayer(p, now)
			}
			continue
		}
		if p.InBattle != 0 {
			continue
		}
		// mark current location as EXPERIENCED if needed
		if p.KnownLocations[loc.Key] < LocKnowExperienced {
			p.KnownLocations[loc.Key] = LocKnowExperienced
			w.Log(Event{T: now, K: "LOC_KNOW_UPDATE", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, P1: p.ID, Msg: "location knowledge updated", Ex: map[string]any{"level": locKnowToStr(LocKnowExperienced)}})
		}
		if w.TryManualPin(p, now) {
			continue
		}
		if now.Before(p.SlowUntil) && w.Rng.Intn(100) < 70 {
			continue
		}
		w.TrySeeEntrances(loc, p, now)

		dx, dy := randStep(w.Rng)
		if dx == 0 && dy == 0 {
			continue
		}
		nx, ny := clamp(p.X+dx, 0, LocSize-1), clamp(p.Y+dy, 0, LocSize-1)
		if !loc.Walkable[nx][ny] {
			continue
		}
		p.X, p.Y = nx, ny

		if e := w.EntranceAt(loc, p.X, p.Y); e != nil {
			w.HandleEntranceTrigger(loc, p, e, now)
			continue
		}
		if g, ok := findGateAt(loc, p.X, p.Y); ok {
			w.TransferPlayer(p, loc, g, now)
			continue
		}
	}

	w.RebuildOcc(loc)
	w.DetectEncounters(loc, now)
	w.ApplySlowZones(loc, now)
}

func (w *World) GetOrCreateLocation(key LocKey) *Location {
	if loc, ok := w.Locations[key]; ok {
		return loc
	}
	loc := &Location{
		Key:       key,
		Players:   map[int]*Player{},
		Occ:       map[[2]int][]int{},
		Kind:      KindField,
		Entrances: []*Entrance{},
	}
	if key.Z == -1 && key.X >= 100000 {
		loc.Kind = KindPocket
		w.GenPocket(loc)
	} else {
		w.GenField(loc)
		w.GenFieldBorderGates(loc)
		w.GenerateEntrancesMaybe(loc)
	}
	w.Locations[key] = loc
	w.Log(Event{T: time.Now(), K: "LOC_CREATE", LX: key.X, LY: key.Y, LZ: key.Z, Msg: "location generated", Ex: map[string]any{
		"kind":      loc.Kind,
		"gates":     len(loc.Gates),
		"entrances": len(loc.Entrances),
	}})
	return loc
}

func (w *World) GenField(loc *Location) {
	for x := 0; x < LocSize; x++ {
		for y := 0; y < LocSize; y++ {
			loc.Walkable[x][y] = true
		}
	}
}

func (w *World) GenFieldBorderGates(loc *Location) {
	lk := loc.Key
	to := LocKey{X: lk.X, Y: lk.Y - 1, Z: lk.Z}
	for x := 0; x < LocSize; x++ {
		loc.Gates = append(loc.Gates, Gate{FromX: x, FromY: 0, ToLoc: to, ToX: x, ToY: LocSize - 1, TwoWay: true, Tag: "north"})
	}
	to = LocKey{X: lk.X, Y: lk.Y + 1, Z: lk.Z}
	for x := 0; x < LocSize; x++ {
		loc.Gates = append(loc.Gates, Gate{FromX: x, FromY: LocSize - 1, ToLoc: to, ToX: x, ToY: 0, TwoWay: true, Tag: "south"})
	}
	to = LocKey{X: lk.X - 1, Y: lk.Y, Z: lk.Z}
	for y := 0; y < LocSize; y++ {
		loc.Gates = append(loc.Gates, Gate{FromX: 0, FromY: y, ToLoc: to, ToX: LocSize - 1, ToY: y, TwoWay: true, Tag: "west"})
	}
	to = LocKey{X: lk.X + 1, Y: lk.Y, Z: lk.Z}
	for y := 0; y < LocSize; y++ {
		loc.Gates = append(loc.Gates, Gate{FromX: LocSize - 1, FromY: y, ToLoc: to, ToX: 0, ToY: y, TwoWay: true, Tag: "east"})
	}
}

func (w *World) GenerateEntrancesMaybe(loc *Location) {
	if w.Rng.Intn(100) >= LocHasEntranceChance {
		return
	}
	x := 2 + w.Rng.Intn(LocSize-4)
	y := 2 + w.Rng.Intn(LocSize-4)

	kind := EntranceSinkhole
	if w.Rng.Intn(100) < CaveChance {
		kind = EntranceCave
	}
	hidden := false
	if kind == EntranceSinkhole && w.Rng.Intn(100) < SinkholeHiddenChance {
		hidden = true
	}
	e := &Entrance{ID: w.NextEntranceID, Kind: kind, X: x, Y: y, Hidden: hidden}
	w.NextEntranceID++
	loc.Entrances = append(loc.Entrances, e)
	w.Log(Event{T: time.Now(), K: "ENTRANCE_SPAWN", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, Msg: "entrance placed", X: x, Y: y, Ex: map[string]any{
		"id": e.ID, "kind": e.Kind, "hidden": e.Hidden,
	}})
}

func (w *World) EntranceAt(loc *Location, x, y int) *Entrance {
	for _, e := range loc.Entrances {
		if e.X == x && e.Y == y {
			return e
		}
	}
	return nil
}

func poiKey(loc LocKey, x, y int, typ string) string {
	return fmt.Sprintf("%d,%d,%d:%d,%d:%s", loc.X, loc.Y, loc.Z, x, y, typ)
}

func (w *World) AddPOIKnowledge(p *Player, key string, lvl KnowledgeLevel, now time.Time, reason string, extra map[string]any) {
	prev, ok := p.KnownPOI[key]
	newKnow := prev
	if !ok {
		newKnow = POIKnow{Level: lvl, Pinned: false, LastUpdate: now}
	} else {
		if lvl > prev.Level {
			newKnow.Level = lvl
		}
		newKnow.LastUpdate = now
	}
	p.KnownPOI[key] = newKnow
	w.Log(Event{T: now, K: "POI_KNOW_UPDATE", LX: p.Loc.X, LY: p.Loc.Y, LZ: p.Loc.Z, P1: p.ID, Msg: reason, Ex: mergeMap(extra, map[string]any{
		"key":    key,
		"level":  newKnow.Level.String(),
		"pinned": newKnow.Pinned,
		"ink":    p.Ink,
		"gold":   p.Gold,
	})})
}

func (w *World) TryManualPin(p *Player, now time.Time) bool {
	if p.Ink <= 0 {
		return false
	}
	if !p.LastPin.IsZero() && now.Sub(p.LastPin) < ManualPinCooldown {
		return false
	}
	type cand struct {
		key string
		kn  POIKnow
	}
	var cands []cand
	for k, kn := range p.KnownPOI {
		if kn.Pinned {
			continue
		}
		cands = append(cands, cand{key: k, kn: kn})
	}
	if len(cands) == 0 {
		return false
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].kn.Level != cands[j].kn.Level {
			return cands[i].kn.Level > cands[j].kn.Level
		}
		return cands[i].kn.LastUpdate.After(cands[j].kn.LastUpdate)
	})
	best := cands[0]
	ch := 0
	switch best.kn.Level {
	case KnowExperienced:
		ch = ManualPinChanceExp
	case KnowSeen:
		ch = ManualPinChanceSeen
	case KnowRumor:
		ch = ManualPinChanceRumor
	}
	p.LastPin = now
	if w.Rng.Intn(100) >= ch {
		return false
	}
	w.Log(Event{T: now, K: "PIN_ACTION", LX: p.Loc.X, LY: p.Loc.Y, LZ: p.Loc.Z, P1: p.ID, Msg: "attempt pin", Ex: map[string]any{"key": best.key, "level": best.kn.Level.String(), "ink": p.Ink}})
	return w.PinPOI(p, best.key, now, "MANUAL_PIN")
}

func (w *World) PinPOI(p *Player, key string, now time.Time, reason string) bool {
	k, ok := p.KnownPOI[key]
	if !ok {
		w.Log(Event{T: now, K: "PIN_FAIL", LX: p.Loc.X, LY: p.Loc.Y, LZ: p.Loc.Z, P1: p.ID, Msg: "unknown POI", Ex: map[string]any{"key": key, "reason": reason}})
		return false
	}
	if k.Pinned {
		return true
	}
	if p.Ink <= 0 {
		w.Log(Event{T: now, K: "PIN_FAIL", LX: p.Loc.X, LY: p.Loc.Y, LZ: p.Loc.Z, P1: p.ID, Msg: "no ink", Ex: map[string]any{"key": key, "reason": reason}})
		return false
	}
	p.Ink--
	k.Pinned = true
	k.LastUpdate = now
	p.KnownPOI[key] = k
	w.Log(Event{T: now, K: "PIN_SUCCESS", LX: p.Loc.X, LY: p.Loc.Y, LZ: p.Loc.Z, P1: p.ID, Msg: "poi pinned", Ex: map[string]any{
		"key": key, "level": k.Level.String(), "inkLeft": p.Ink, "reason": reason,
	}})
	return true
}

func (w *World) TrySeeEntrances(loc *Location, p *Player, now time.Time) {
	for _, e := range loc.Entrances {
		if e.Kind == EntranceCave {
			if abs(e.X-p.X) <= 1 && abs(e.Y-p.Y) <= 1 {
				key := poiKey(loc.Key, e.X, e.Y, "CAVE")
				w.MarkPOISeen(p, key, now, map[string]any{
					"x": e.X, "y": e.Y, "entranceId": e.ID,
				})
			}
		}
	}
}

func (w *World) HandleEntranceTrigger(loc *Location, p *Player, e *Entrance, now time.Time) {
	if e.Kind == EntranceCave {
		if w.Rng.Intn(100) >= 35 {
			return
		}
	}
	typ := "SINKHOLE"
	if e.Kind == EntranceCave {
		typ = "CAVE"
	}
	key := poiKey(loc.Key, e.X, e.Y, typ)
	w.AddPOIKnowledge(p, key, KnowExperienced, now, "experienced entrance", map[string]any{
		"x": e.X, "y": e.Y, "entranceId": e.ID, "kind": e.Kind, "hidden": e.Hidden,
	})
	w.TryGrantPOILoot(p, key, e, now)
	if e.PocketID == 0 {
		e.PocketID = w.NextPocketID
		w.NextPocketID++
		w.CreatePocketForEntrance(loc, e, now)
	}
	pocketKey := LocKey{X: 100000 + e.PocketID, Y: 0, Z: -1}
	pocket := w.GetOrCreateLocation(pocketKey)
	w.MovePlayerToLocation(p, loc, pocket, 10, 10, now, "ENTRANCE_ENTER", map[string]any{"entranceId": e.ID, "pocketId": e.PocketID, "kind": e.Kind})
}

func (w *World) CreatePocketForEntrance(surface *Location, e *Entrance, now time.Time) {
	pocketKey := LocKey{X: 100000 + e.PocketID, Y: 0, Z: -1}
	pocket := w.GetOrCreateLocation(pocketKey)
	pocket.Kind = KindPocket

	exitCount := PocketMinExits
	if PocketMaxExits > PocketMinExits {
		exitCount = PocketMinExits + w.Rng.Intn(PocketMaxExits-PocketMinExits+1)
	}
	for i := 0; i < exitCount; i++ {
		ex, ey := w.RandomWalkableInPocketFarFrom(pocket, 10, 10, 25)
		toLoc := surface.Key
		if w.Rng.Intn(100) < ExitToOtherLocChance {
			dirs := []LocKey{{0, -1, 0}, {1, 0, 0}, {0, 1, 0}, {-1, 0, 0}}
			d := dirs[w.Rng.Intn(len(dirs))]
			toLoc = LocKey{X: surface.Key.X + d.X, Y: surface.Key.Y + d.Y, Z: 0}
		}
		dst := w.GetOrCreateLocation(toLoc)
		tx, ty := w.RandomWalkableFarFrom(dst, e.X, e.Y, 30)

		pocket.Gates = append(pocket.Gates, Gate{FromX: ex, FromY: ey, ToLoc: toLoc, ToX: tx, ToY: ty, TwoWay: false, Tag: "pocket_exit"})
		w.Log(Event{T: now, K: "POCKET_EXIT_ADD", LX: pocket.Key.X, LY: pocket.Key.Y, LZ: pocket.Key.Z, Msg: "exit created", X: ex, Y: ey, Ex: map[string]any{"toLX": toLoc.X, "toLY": toLoc.Y, "toLZ": toLoc.Z, "toX": tx, "toY": ty}})
	}
	w.Log(Event{T: now, K: "POCKET_BIND", LX: surface.Key.X, LY: surface.Key.Y, LZ: surface.Key.Z, Msg: "entrance bound to pocket", X: e.X, Y: e.Y, Ex: map[string]any{"entranceId": e.ID, "pocketId": e.PocketID, "exits": exitCount}})
}

func (w *World) GenPocket(loc *Location) {
	for x := 0; x < LocSize; x++ {
		for y := 0; y < LocSize; y++ {
			loc.Walkable[x][y] = false
		}
	}
	for x := 8; x <= 30; x++ {
		for y := 8; y <= 30; y++ {
			loc.Walkable[x][y] = true
		}
	}
	for x := 30; x <= 65; x++ {
		loc.Walkable[x][19] = true
		loc.Walkable[x][20] = true
	}
	for x := 62; x <= 78; x++ {
		for y := 14; y <= 28; y++ {
			loc.Walkable[x][y] = true
		}
	}
}

func (w *World) RandomWalkableInPocketFarFrom(pocket *Location, ox, oy int, minDist int) (int, int) {
	for tries := 0; tries < 2000; tries++ {
		x := w.Rng.Intn(LocSize)
		y := w.Rng.Intn(LocSize)
		if !pocket.Walkable[x][y] {
			continue
		}
		if abs(x-ox)+abs(y-oy) < minDist {
			continue
		}
		return x, y
	}
	return 70, 20
}

func (w *World) RandomWalkableFarFrom(loc *Location, ox, oy int, minDist int) (int, int) {
	for tries := 0; tries < 2500; tries++ {
		x := 2 + w.Rng.Intn(LocSize-4)
		y := 2 + w.Rng.Intn(LocSize-4)
		if !loc.Walkable[x][y] {
			continue
		}
		if abs(x-ox)+abs(y-oy) < minDist {
			continue
		}
		return x, y
	}
	return 1, 1
}

func findGateAt(loc *Location, x, y int) (Gate, bool) {
	for _, g := range loc.Gates {
		if g.FromX == x && g.FromY == y {
			return g, true
		}
	}
	return Gate{}, false
}

func (w *World) TransferPlayer(p *Player, from *Location, g Gate, now time.Time) {
	to := w.GetOrCreateLocation(g.ToLoc)
	w.MovePlayerToLocation(p, from, to, g.ToX, g.ToY, now, "LOC_MOVE", map[string]any{"tag": g.Tag})
}

func (w *World) MovePlayerToLocation(p *Player, from *Location, to *Location, x, y int, now time.Time, reason string, ex map[string]any) {
	delete(from.Players, p.ID)
	w.RebuildOcc(from)

	p.Loc = to.Key
	p.X, p.Y = x, y
	to.Players[p.ID] = p
	w.RebuildOcc(to)

	// knowledge: mark destination as EXPERIENCED
	cur := p.KnownLocations[to.Key]
	if cur < LocKnowExperienced {
		p.KnownLocations[to.Key] = LocKnowExperienced
		w.Log(Event{T: now, K: "LOC_KNOW_UPDATE", LX: to.Key.X, LY: to.Key.Y, LZ: to.Key.Z, P1: p.ID,
			Msg: "location knowledge updated", Ex: map[string]any{"level": locKnowToStr(LocKnowExperienced)}})
	}
	// neighbors become SEEN (without forcing generation)
	if to.Key.Z == 0 {
		neighbors := []LocKey{{X: to.Key.X + 1, Y: to.Key.Y, Z: 0}, {X: to.Key.X - 1, Y: to.Key.Y, Z: 0}, {X: to.Key.X, Y: to.Key.Y + 1, Z: 0}, {X: to.Key.X, Y: to.Key.Y - 1, Z: 0}}
		for _, nk := range neighbors {
			if p.KnownLocations[nk] < LocKnowSeen {
				p.KnownLocations[nk] = LocKnowSeen
				w.Log(Event{T: now, K: "LOC_KNOW_UPDATE", LX: nk.X, LY: nk.Y, LZ: nk.Z, P1: p.ID,
					Msg: "location knowledge updated", Ex: map[string]any{"level": locKnowToStr(LocKnowSeen)}})
			}
		}
	}
	w.Log(Event{T: now, K: reason, LX: to.Key.X, LY: to.Key.Y, LZ: to.Key.Z, P1: p.ID, X: p.X, Y: p.Y, Msg: "moved", Ex: ex})
}

func (w *World) RespawnPlayer(p *Player, now time.Time) {
	// very simple respawn: move to start location with fresh HP and short invulnerability
	from := w.GetOrCreateLocation(p.Loc)
	toKey := LocKey{X: StartWorldX, Y: StartWorldY, Z: 0}
	to := w.GetOrCreateLocation(toKey)

	delete(from.Players, p.ID)
	w.RebuildOcc(from)

	p.Alive = true
	p.HP = BaseHP
	p.InBattle = 0
	p.DeadUntil = time.Time{}
	p.SpawnProtect = now.Add(SpawnInvuln)

	p.Loc = to.Key
	p.X = w.Rng.Intn(LocSize)
	p.Y = w.Rng.Intn(LocSize)
	to.Players[p.ID] = p
	w.RebuildOcc(to)

	w.Log(Event{T: now, K: "RESPAWN", LX: to.Key.X, LY: to.Key.Y, LZ: to.Key.Z, P1: p.ID, X: p.X, Y: p.Y, Msg: "respawned", Ex: map[string]any{"invulnSec": int(SpawnInvuln.Seconds())}})
}

func (w *World) RebuildOcc(loc *Location) {
	loc.Occ = map[[2]int][]int{}
	for _, p := range loc.Players {
		if !p.Alive {
			continue
		}
		cell := [2]int{p.X, p.Y}
		loc.Occ[cell] = append(loc.Occ[cell], p.ID)
	}
}

func (w *World) ForgetSweepIfNeeded() {
	now := time.Now()
	if !w.LastForgetSweep.IsZero() && now.Sub(w.LastForgetSweep) < ForgetSweepEvery {
		return
	}
	w.LastForgetSweep = now
	for _, loc := range w.Locations {
		for _, p := range loc.Players {
			for key, know := range p.KnownPOI {
				if know.Pinned {
					continue
				}
				age := now.Sub(know.LastUpdate)
				ttl := ForgetExperiencedAfter
				switch know.Level {
				case KnowRumor:
					ttl = ForgetRumorAfter
				case KnowSeen:
					ttl = ForgetSeenAfter
				case KnowExperienced:
					ttl = ForgetExperiencedAfter
				}
				if age >= ttl {
					delete(p.KnownPOI, key)
					w.Log(Event{T: now, K: "FORGET_POI", LX: p.Loc.X, LY: p.Loc.Y, LZ: p.Loc.Z, P1: p.ID, Msg: "forgot POI", Ex: map[string]any{
						"key": key, "level": know.Level.String(), "ageSec": int(age.Seconds()),
					}})
				}
			}
			// sweep perception (SeenPOI)
			for key, t := range p.SeenPOI {
				age := now.Sub(t)
				if age >= SeenPOITTLSec {
					delete(p.SeenPOI, key)
					// и заодно сбросим счётчик, если хотим окно накопления:
					delete(p.SeenPOICount, key)
				}
			}
		}
	}
}

func (w *World) DetectEncounters(loc *Location, now time.Time) {
	for cell, ids := range loc.Occ {
		if len(ids) < 2 {
			continue
		}
		p1, p2 := ids[0], ids[1]
		a, b := loc.Players[p1], loc.Players[p2]
		if a == nil || b == nil || !a.Alive || !b.Alive {
			continue
		}
		if now.Before(a.SpawnProtect) || now.Before(b.SpawnProtect) {
			continue
		}
		if w.Rng.Intn(100) < ParleyChanceOnMeet {
			if w.TryTradeMapInfo(loc, a, b, now) {
				continue
			}
		}
		bt := w.StartBattle(loc.Key, cell[0], cell[1], []int{p1, p2}, now)
		a.InBattle, b.InBattle = bt.ID, bt.ID
		w.Log(Event{T: now, K: "BATTLE_START", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, B: bt.ID, P1: p1, P2: p2, X: cell[0], Y: cell[1], Msg: "collision -> battle"})
	}
}

func (w *World) TryTradeMapInfo(loc *Location, a, b *Player, now time.Time) bool {
	type offer struct {
		seller *Player
		buyer  *Player
		key    string
		lvl    KnowledgeLevel
		price  int
	}
	var offers []offer
	addOffers := func(seller, buyer *Player) {
		for key, kn := range seller.KnownPOI {
			if !kn.Pinned {
				continue
			}
			if _, ok := buyer.KnownPOI[key]; ok {
				continue
			}
			price := MapBuyRumorPrice
			switch kn.Level {
			case KnowSeen:
				price = MapBuySeenPrice
			case KnowExperienced:
				price = MapBuyExpPrice
			}
			if buyer.Gold < price {
				continue
			}
			offers = append(offers, offer{seller: seller, buyer: buyer, key: key, lvl: kn.Level, price: price})
		}
	}
	addOffers(a, b)
	addOffers(b, a)
	if len(offers) == 0 {
		w.Log(Event{T: now, K: "PARLEY_FAIL", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, P1: a.ID, P2: b.ID, Msg: "no trade possible"})
		return false
	}
	off := offers[w.Rng.Intn(len(offers))]
	w.Log(Event{T: now, K: "TRADE_OFFER", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, P1: off.seller.ID, P2: off.buyer.ID, Msg: "map info offer", Ex: map[string]any{"key": off.key, "level": off.lvl.String(), "price": off.price}})
	off.buyer.Gold -= off.price
	off.seller.Gold += off.price
	w.AddPOIKnowledge(off.buyer, off.key, off.lvl, now, "bought map info", map[string]any{"from": off.seller.ID, "price": off.price})
	w.Log(Event{T: now, K: "TRADE_DONE", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, P1: off.seller.ID, P2: off.buyer.ID, Msg: "trade complete", Ex: map[string]any{"key": off.key, "level": off.lvl.String(), "price": off.price}})
	return true
}

// ------------------- MARKET (POI v1) -------------------

// CreatePOIListing lists a POI key that the seller already knows.
// Recommended: require that the POI is pinned (costs ink), so listings are valuable and limited.
func (w *World) CreatePOIListing(seller *Player, key string, price int, uses int, now time.Time) (*POIListing, error) {
	if seller == nil || !seller.Alive {
		return nil, fmt.Errorf("seller not alive")
	}
	kn, ok := seller.KnownPOI[key]
	if !ok {
		return nil, fmt.Errorf("seller doesn't know POI")
	}
	if !kn.Pinned {
		return nil, fmt.Errorf("POI must be pinned to sell")
	}
	if price <= 0 {
		// sensible defaults by knowledge quality
		price = MapBuyRumorPrice
		switch kn.Level {
		case KnowSeen:
			price = MapBuySeenPrice
		case KnowExperienced:
			price = MapBuyExpPrice
		}
	}
	if uses <= 0 {
		uses = 10
	}
	id := w.NextPOIListing
	w.NextPOIListing++
	l := &POIListing{
		ID:       id,
		SellerID: seller.ID,
		Key:      key,
		Level:    kn.Level,
		Pinned:   kn.Pinned,
		Price:    price,
		UsesLeft: uses,
		Created:  now,
	}
	w.MarketPOI[id] = l
	w.Log(Event{T: now, K: "MARKET_POI_CREATE", LX: seller.Loc.X, LY: seller.Loc.Y, LZ: seller.Loc.Z, P1: seller.ID, Msg: "poi listed", Ex: map[string]any{
		"id": id, "key": key, "price": price, "uses": uses, "level": kn.Level.String(), "pinned": kn.Pinned,
	}})
	return l, nil
}

func (w *World) ListPOIListings() []*POIListing {
	out := make([]*POIListing, 0, len(w.MarketPOI))
	for _, l := range w.MarketPOI {
		if l == nil || l.UsesLeft <= 0 {
			continue
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// BuyPOIListing purchases a listing by id.
// Buyer gains POI knowledge (at least the listing's knowledge level).
func (w *World) BuyPOIListing(buyer *Player, listingID int64, now time.Time) (*POIListing, error) {
	if buyer == nil || !buyer.Alive {
		return nil, fmt.Errorf("buyer not alive")
	}
	l := w.MarketPOI[listingID]
	if l == nil || l.UsesLeft <= 0 {
		return nil, fmt.Errorf("listing not found")
	}
	if buyer.ID == l.SellerID {
		return nil, fmt.Errorf("cannot buy your own listing")
	}
	if _, ok := buyer.KnownPOI[l.Key]; ok {
		return nil, fmt.Errorf("buyer already knows this POI")
	}
	if buyer.Gold < l.Price {
		return nil, fmt.Errorf("not enough gold")
	}
	seller := w.FindPlayer(l.SellerID)
	if seller == nil {
		return nil, fmt.Errorf("seller offline")
	}

	// money transfer
	buyer.Gold -= l.Price
	seller.Gold += l.Price

	// knowledge transfer
	w.AddPOIKnowledge(buyer, l.Key, l.Level, now, "bought poi from market", map[string]any{"from": seller.ID, "price": l.Price, "listingId": l.ID})

	// consume listing
	l.UsesLeft--
	if l.UsesLeft <= 0 {
		delete(w.MarketPOI, l.ID)
	}

	w.Log(Event{T: now, K: "MARKET_POI_BUY", LX: buyer.Loc.X, LY: buyer.Loc.Y, LZ: buyer.Loc.Z, P1: buyer.ID, P2: seller.ID, Msg: "poi purchased", Ex: map[string]any{
		"id": l.ID, "key": l.Key, "price": l.Price, "level": l.Level.String(), "usesLeft": max(0, l.UsesLeft),
	}})
	return l, nil
}

func (w *World) StartBattle(locKey LocKey, x, y int, participants []int, now time.Time) *Battle {
	id := w.NextBattleID
	w.NextBattleID++
	b := &Battle{
		ID:                   id,
		Loc:                  locKey,
		Cells:                map[[2]int]bool{{x, y}: true},
		Phase:                PhasePositioning,
		PositionRoundsRemain: PosRounds,
		Participants:         map[int]int{},
	}
	for i, pid := range participants {
		side := +1
		if i == 0 {
			side = -1
		}
		b.Participants[pid] = side
	}
	for pid := range b.Participants {
		b.TurnOrder = append(b.TurnOrder, pid)
	}
	sort.Ints(b.TurnOrder)
	w.Battles[b.ID] = b
	w.Log(Event{T: now, K: "BATTLE_PHASE", LX: locKey.X, LY: locKey.Y, LZ: locKey.Z, B: b.ID, Msg: string(b.Phase)})
	return b
}

func (w *World) UpdateBattles(loc *Location, now time.Time) {
	for id, b := range w.Battles {
		if b.Loc != loc.Key {
			continue
		}
		w.StepBattleTurn(loc, b, now)
		alive := 0
		for pid := range b.Participants {
			if p := loc.Players[pid]; p != nil && p.Alive {
				alive++
			}
		}
		if alive < 2 {
			w.Log(Event{T: now, K: "BATTLE_END", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, B: b.ID, Msg: "battle ended"})
			for pid := range b.Participants {
				if p := loc.Players[pid]; p != nil && p.Alive {
					p.InBattle = 0
				}
			}
			delete(w.Battles, id)
		}
	}
}

func (w *World) StepBattleTurn(loc *Location, b *Battle, now time.Time) {
	order := make([]int, 0, len(b.TurnOrder))
	for _, pid := range b.TurnOrder {
		if p := loc.Players[pid]; p != nil && p.Alive {
			order = append(order, pid)
		}
	}
	if len(order) < 2 {
		return
	}
	b.TurnOrder = order
	if b.TurnIndex >= len(b.TurnOrder) {
		b.TurnIndex = 0
	}
	actorID := b.TurnOrder[b.TurnIndex]
	actor := loc.Players[actorID]
	actor.Defending = false

	act := w.ChooseAction()
	w.Log(Event{T: now, K: "TURN", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, B: b.ID, P1: actorID, Msg: string(act), Ex: map[string]any{"phase": b.Phase}})

	switch act {
	case ActDefend, ActPosition:
		actor.Defending = true
	case ActWithdraw:
		delete(b.Participants, actorID)
		actor.InBattle = 0
		w.Log(Event{T: now, K: "WITHDRAW", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, B: b.ID, P1: actorID, Msg: "left battle"})
		return
	case ActAttack:
		targetID := w.PickTarget(loc, b, actorID)
		if targetID == 0 {
			actor.Defending = true
			break
		}
		dmg := BaseDamage
		if loc.Players[targetID].Defending {
			dmg = max(1, dmg/2)
		}
		loc.Players[targetID].HP -= dmg
		w.Log(Event{T: now, K: "DAMAGE", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, B: b.ID, P1: actorID, P2: targetID, Msg: "hit", Ex: map[string]any{"dmg": dmg, "hp": loc.Players[targetID].HP}})
		if loc.Players[targetID].HP <= 0 {
			loc.Players[targetID].Alive = false
			loc.Players[targetID].InBattle = 0
			loc.Players[targetID].DeadUntil = now.Add(5 * time.Second)
			delete(b.Participants, targetID)
			w.Log(Event{T: now, K: "DEATH", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, B: b.ID, P1: targetID, P2: actorID, Msg: "died", Ex: map[string]any{"respawnInSec": 5}})
		}
	}

	b.TurnIndex++
	if b.TurnIndex >= len(b.TurnOrder) {
		b.TurnIndex = 0
		if b.Phase == PhasePositioning {
			b.PositionRoundsRemain--
			if b.PositionRoundsRemain <= 0 {
				b.Phase = PhaseControl
				w.Log(Event{T: now, K: "BATTLE_PHASE", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, B: b.ID, Msg: string(b.Phase)})
			}
		}
	}
}

func (w *World) ChooseAction() Action {
	r := w.Rng.Intn(100)
	if r < 55 {
		return ActAttack
	}
	if r < 75 {
		return ActDefend
	}
	if r < 90 {
		return ActPosition
	}
	return ActWithdraw
}

func (w *World) PickTarget(loc *Location, b *Battle, actorID int) int {
	actorSide := b.Participants[actorID]
	var enemies []int
	for pid, side := range b.Participants {
		if pid == actorID {
			continue
		}
		p := loc.Players[pid]
		if p == nil || !p.Alive {
			continue
		}
		if side != actorSide {
			enemies = append(enemies, pid)
		}
	}
	if len(enemies) == 0 {
		return 0
	}
	return enemies[w.Rng.Intn(len(enemies))]
}

func (w *World) ApplySlowZones(loc *Location, now time.Time) {
	for _, b := range w.Battles {
		if b.Loc != loc.Key || b.Phase != PhaseControl {
			continue
		}
		for cell := range b.Cells {
			for dx := -1; dx <= 1; dx++ {
				for dy := -1; dy <= 1; dy++ {
					if dx == 0 && dy == 0 {
						continue
					}
					x, y := cell[0]+dx, cell[1]+dy
					if x < 0 || y < 0 || x >= LocSize || y >= LocSize {
						continue
					}
					ids := loc.Occ[[2]int{x, y}]
					for _, pid := range ids {
						p := loc.Players[pid]
						if p == nil || !p.Alive || p.InBattle != 0 {
							continue
						}
						until := now.Add(SlowDuration)
						if until.After(p.SlowUntil) {
							p.SlowUntil = until
							w.Log(Event{T: now, K: "SLOW", LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z, P1: pid, B: b.ID, X: x, Y: y, Msg: "slow applied"})
						}
					}
				}
			}
		}
	}
}

func (w *World) Log(e Event) {
	enc := json.NewEncoder(w.LogFile)
	_ = enc.Encode(e)
	if w.OnEvent != nil {
		w.OnEvent(e)
	}
}

func randStep(r *rand.Rand) (int, int) {
	switch r.Intn(9) {
	case 0:
		return 0, 0
	case 1:
		return 1, 0
	case 2:
		return -1, 0
	case 3:
		return 0, 1
	case 4:
		return 0, -1
	case 5:
		return 1, 1
	case 6:
		return 1, -1
	case 7:
		return -1, 1
	default:
		return -1, -1
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mergeMap(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func (w *World) TryGrantPOILoot(p *Player, poi string, e *Entrance, now time.Time) {
	if p.LastPOILoot == nil {
		p.LastPOILoot = map[string]time.Time{}
	}
	if t, ok := p.LastPOILoot[poi]; ok {
		if now.Sub(t) < POILootCooldown {
			return
		}
	}

	p.LastPOILoot[poi] = now

	// базовые значения
	gold := 0
	ink := 0
	hpDelta := 0

	switch e.Kind {
	case EntranceCave:
		// почти всегда немного золота
		gold = 1 + w.Rng.Intn(3) // 1..3
		if w.Rng.Intn(100) < 18 {
			ink = 1
		}

	case EntranceSinkhole:
		// риск
		if w.Rng.Intn(100) < 55 {
			hpDelta = -(1 + w.Rng.Intn(5)) // -1..-5
		}
		// шанс “джекпота”
		if w.Rng.Intn(100) < 35 {
			gold = 3 + w.Rng.Intn(8) // 3..10
		} else {
			gold = w.Rng.Intn(2) // 0..1
		}
		if w.Rng.Intn(100) < 30 {
			ink = 1
		}
	}

	if gold == 0 && ink == 0 && hpDelta == 0 {
		return
	}

	p.Gold += gold
	p.Ink += ink
	if hpDelta != 0 {
		p.HP += hpDelta
		if p.HP <= 0 {
			// смерть от ловушки: аккуратно, чтобы не ломать бой/логику
			p.Alive = false
			p.InBattle = 0
			p.DeadUntil = now.Add(5 * time.Second)
			w.Log(Event{T: now, K: "DEATH", LX: p.Loc.X, LY: p.Loc.Y, LZ: p.Loc.Z, P1: p.ID, Msg: "died from sinkhole", Ex: map[string]any{"respawnInSec": 5}})
		}
	}

	w.Log(Event{
		T: now, K: "POI_LOOT",
		LX: p.Loc.X, LY: p.Loc.Y, LZ: p.Loc.Z,
		P1:  p.ID,
		Msg: "poi loot granted",
		Ex: map[string]any{
			"poi":         poi,
			"kind":        string(e.Kind),
			"gold":        gold,
			"ink":         ink,
			"hpDelta":     hpDelta,
			"hp":          p.HP,
			"cooldownSec": int(POILootCooldown.Seconds()),
		},
	})
}

func (w *World) MarkPOISeen(p *Player, key string, now time.Time, extra map[string]any) {
	if p.SeenPOI == nil {
		p.SeenPOI = map[string]time.Time{}
	}
	if p.SeenPOICount == nil {
		p.SeenPOICount = map[string]int{}
	}

	// обновляем "видел недавно"
	p.SeenPOI[key] = now

	// накапливаем счётчик, но только если не слишком давно последний раз видел
	// (простая логика: если запись была очень старой — считаем как заново)
	//last, ok := p.SeenPOI[key]
	//_ = ok
	// last == now тут, поэтому для окна используем отдельное: будем сбрасывать в sweep (см. ниже)

	p.SeenPOICount[key]++

	w.Log(Event{
		T: now, K: "POI_SEEN",
		LX: p.Loc.X, LY: p.Loc.Y, LZ: p.Loc.Z,
		P1:  p.ID,
		Msg: "poi seen",
		Ex: mergeMap(extra, map[string]any{
			"key":   key,
			"count": p.SeenPOICount[key],
		}),
	})

	// если уже знает — ничего не делаем
	if p.KnownPOI != nil {
		if kn, ok := p.KnownPOI[key]; ok && kn.Level >= KnowSeen {
			return
		}
	}

	// перевод "видел много раз" -> KnowSeen
	if p.SeenPOICount[key] >= SeenToKnownCount {
		w.AddPOIKnowledge(p, key, KnowSeen, now, "learned poi by repeated sightings", map[string]any{
			"seenCount": p.SeenPOICount[key],
		})
	}
}

func (w *World) PickRandomPinnedPOIKey(p *Player) (string, bool) {
	if p.KnownPOI == nil || len(p.KnownPOI) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(p.KnownPOI))
	for k, know := range p.KnownPOI {
		if know.Pinned && know.Level >= KnowSeen { // или твой уровень
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return "", false
	}
	return keys[w.Rng.Intn(len(keys))], true
}
