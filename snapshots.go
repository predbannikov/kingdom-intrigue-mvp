package main

import (
	"sort"
)

// WorldSnapshot — публичный контракт для UI (/api/world).
// Менять поля/имена нельзя без версии или отдельного endpoint.
type WorldSnapshot struct {
	Tick        int `json:"tick"`
	Z           int `json:"z"`
	Battles     int `json:"battles"`
	PlayersLive int `json:"playersLive"`

	MinLX int `json:"minLX"`
	MaxLX int `json:"maxLX"`
	MinLY int `json:"minLY"`
	MaxLY int `json:"maxLY"`

	Cells []LocSummary `json:"cells"`
	Top   []LocSummary `json:"top"`
	
	Paused bool `json:"paused"`
}

type LocSummary struct {
	LX      int `json:"lx"`
	LY      int `json:"ly"`
	LZ      int `json:"lz"`
	Players int `json:"players"`
	Battles int `json:"battles"`

	Entrances int `json:"entrances"`
	Caves     int `json:"caves"`
	Sinkholes int `json:"sinkholes"`
	Hidden    int `json:"hidden"`

	KnownBy       int `json:"knownBy"`
	ExperiencedBy int `json:"experiencedBy"`
}


func BuildWorldSnapshot(w *World, tick int, zFilter int) WorldSnapshot {
	out := WorldSnapshot{
		Tick: tick,
		Z:    zFilter,
		Battles: len(w.Battles),
		MinLX: 0, MaxLX: -1,
		MinLY: 0, MaxLY: -1,
	}

	// gather all players once (unique by id)
	allPlayers := map[int]*Player{}
	for _, loc := range w.Locations {
		for id, p := range loc.Players {
			allPlayers[id] = p
		}
	}
	for _, p := range allPlayers {
		if p != nil && p.Alive {
			out.PlayersLive++
		}
	}

	cells := make([]LocSummary, 0, 256)
	for _, loc := range w.Locations {
		if loc.Key.Z != zFilter {
			continue
		}
		if len(loc.Players) == 0 {
			continue
		}

		bc := 0
		for _, b := range w.Battles {
			if b.Loc == loc.Key {
				bc++
			}
		}

		// entrances stats
		caves := 0
		sink := 0
		hidden := 0
		for _, e := range loc.Entrances {
			if e.Hidden {
				hidden++
			}
			if e.Kind == EntranceCave {
				caves++
			} else if e.Kind == EntranceSinkhole {
				sink++
			}
		}

		// knowledge stats (counts across all players)
		knownBy := 0
		expBy := 0
		for _, p := range allPlayers {
			if p == nil || p.KnownLocations == nil {
				continue
			}
			lv := p.KnownLocations[loc.Key]
			if lv >= LocKnowSeen {
				knownBy++
			}
			if lv >= LocKnowExperienced {
				expBy++
			}
		}

		s := LocSummary{
			LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z,
			Players: len(loc.Players), Battles: bc,
			Entrances: len(loc.Entrances),
			Caves: caves, Sinkholes: sink, Hidden: hidden,
			KnownBy: knownBy, ExperiencedBy: expBy,
		}
		cells = append(cells, s)

		if out.MaxLX < out.MinLX { // first
			out.MinLX, out.MaxLX = s.LX, s.LX
			out.MinLY, out.MaxLY = s.LY, s.LY
		} else {
			if s.LX < out.MinLX { out.MinLX = s.LX }
			if s.LX > out.MaxLX { out.MaxLX = s.LX }
			if s.LY < out.MinLY { out.MinLY = s.LY }
			if s.LY > out.MaxLY { out.MaxLY = s.LY }
		}
	}

	out.Cells = cells

	// Top list (by players desc, then battles)
	tmp := make([]LocSummary, len(cells))
	copy(tmp, cells)
	sort.Slice(tmp, func(i, j int) bool {
		if tmp[i].Players != tmp[j].Players {
			return tmp[i].Players > tmp[j].Players
		}
		if tmp[i].Battles != tmp[j].Battles {
			return tmp[i].Battles > tmp[j].Battles
		}
		if tmp[i].LY != tmp[j].LY {
			return tmp[i].LY < tmp[j].LY
		}
		return tmp[i].LX < tmp[j].LX
	})
	if len(tmp) > 250 {
		out.Top = tmp[:250]
	} else {
		out.Top = tmp
	}
	return out
}



type PlayerSnapshot struct {
	ID       int  `json:"id"`
	LX       int  `json:"lx"`
	LY       int  `json:"ly"`
	LZ       int  `json:"lz"`
	X        int  `json:"x"`
	Y        int  `json:"y"`
	Alive    bool `json:"alive"`
	InBattle int  `json:"inBattle"`
	HP       int  `json:"hp"`
	Ink      int  `json:"ink"`
	Gold     int  `json:"gold"`

	KnownLoc int `json:"knownLoc"`
	KnownPOI int `json:"knownPOI"`
}

func BuildPlayerSnapshot(p *Player) PlayerSnapshot {
	return PlayerSnapshot{
		ID: p.ID,
		LX: p.Loc.X, LY: p.Loc.Y, LZ: p.Loc.Z,
		X: p.X, Y: p.Y,
		Alive:    p.Alive,
		InBattle: p.InBattle,
		HP:       p.HP,
		Ink:      p.Ink,
		Gold:     p.Gold,
		KnownLoc: len(p.KnownLocations),
		KnownPOI: len(p.KnownPOI),
	}
}

type LocationSnapshot struct {
	LX int    `json:"lx"`
	LY int    `json:"ly"`
	LZ int    `json:"lz"`
	Kind string `json:"kind"`

	Players []PlayerInLoc `json:"players"`
	Entrances []EntranceSnap `json:"entrances"`
	Gates int `json:"gates"`
	ActiveBattles []int `json:"activeBattles"`
}

type PlayerInLoc struct {
	ID int `json:"id"`
	X int `json:"x"`
	Y int `json:"y"`
	Alive bool `json:"alive"`
	HP int `json:"hp"`
	InBattle int `json:"inBattle"`
}

type EntranceSnap struct {
	ID int `json:"id"`
	Kind string `json:"kind"`
	X int `json:"x"`
	Y int `json:"y"`
	Hidden bool `json:"hidden"`
	PocketID int `json:"pocketId"`
}

func BuildLocationSnapshot(loc *Location, w *World) LocationSnapshot {
	out := LocationSnapshot{
		LX: loc.Key.X, LY: loc.Key.Y, LZ: loc.Key.Z,
		Kind: string(loc.Kind),
		Gates: len(loc.Gates),
	}
	for _, p := range loc.Players {
		out.Players = append(out.Players, PlayerInLoc{ID: p.ID, X: p.X, Y: p.Y, Alive: p.Alive, HP: p.HP, InBattle: p.InBattle})
	}
	sort.Slice(out.Players, func(i,j int) bool { return out.Players[i].ID < out.Players[j].ID })
	if len(out.Players) > 500 {
		out.Players = out.Players[:500]
	}
	for _, e := range loc.Entrances {
		out.Entrances = append(out.Entrances, EntranceSnap{
			ID: e.ID, Kind: string(e.Kind), X: e.X, Y: e.Y, Hidden: e.Hidden, PocketID: e.PocketID,
		})
	}
	for id, b := range w.Battles {
		if b.Loc == loc.Key {
			out.ActiveBattles = append(out.ActiveBattles, id)
		}
	}
	sort.Ints(out.ActiveBattles)
	return out
}
