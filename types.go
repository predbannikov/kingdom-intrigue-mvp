package main

import (
	"math/rand"
	"os"
	"time"
)

const (
	LocSize = 100

	PosRounds    = 2
	SlowDuration = 1200 * time.Millisecond

	BaseHP     = 30
	BaseDamage = 6

	LocHasEntranceChance = 25 // %
	CaveChance           = 70 // %
	SinkholeHiddenChance = 40 // %

	PocketMinExits       = 1
	PocketMaxExits       = 2
	ExitToOtherLocChance = 25 // %

	StartWorldX = 2
	StartWorldY = 2

	ForgetRumorAfter       = 10 * time.Second
	ForgetSeenAfter        = 30 * time.Second
	ForgetExperiencedAfter = 90 * time.Second
	ForgetSweepEvery       = 2 * time.Second

	ManualPinCooldown     = 3 * time.Second
	ManualPinChanceSeen   = 10
	ManualPinChanceExp    = 45
	ManualPinChanceRumor  = 2
	SpawnInvuln           = 5 * time.Second

	ParleyChanceOnMeet 	= 18
	MapBuyRumorPrice   	= 2
	MapBuySeenPrice    	= 4
	MapBuyExpPrice     	= 7
	
	POILootCooldown 	= 20 * time.Second
	
	SeenPOITTLSec      = 6 * time.Second    // сколько “держится в голове” факт, что видел
	SeenToKnownCount   = 3                  // сколько раз увидеть, чтобы стало KnowSeen
	SeenToKnownWindow  = 20 * time.Second   // окно накопления
)

type Phase string

const (
	PhasePositioning Phase = "POSITIONING"
	PhaseControl     Phase = "CONTROL"
)

type Action string

const (
	ActAttack   Action = "ATTACK"
	ActDefend   Action = "DEFEND"
	ActPosition Action = "POSITION"
	ActWithdraw Action = "WITHDRAW"
)

type LocKind string

const (
	KindField  LocKind = "FIELD"
	KindPocket LocKind = "POCKET"
)

type EntranceKind string

const (
	EntranceCave     EntranceKind = "CAVE"
	EntranceSinkhole EntranceKind = "SINKHOLE"
)

type LocKey struct{ X, Y, Z int }

type KnowledgeLevel int

const (
	KnowRumor KnowledgeLevel = iota
	KnowSeen
	KnowExperienced
)

func (k KnowledgeLevel) String() string {
	switch k {
	case KnowRumor:
		return "RUMOR"
	case KnowSeen:
		return "SEEN"
	case KnowExperienced:
		return "EXPERIENCED"
	default:
		return "UNKNOWN"
	}
}

type POIKnow struct {
	Level      KnowledgeLevel `json:"level"`
	Pinned     bool           `json:"pinned"`
	LastUpdate time.Time      `json:"lastUpdate"`
}

type LocKnowLevel int

const (
	LocKnowNone LocKnowLevel = iota
	LocKnowSeen
	LocKnowExperienced
)

func locKnowToStr(l LocKnowLevel) string {
	switch l {
	case LocKnowExperienced:
		return "EXPERIENCED"
	case LocKnowSeen:
		return "SEEN"
	default:
		return "NONE"
	}
}

type Event struct {
	T   time.Time      `json:"t"`
	K   string         `json:"k"`
	LX  int            `json:"lx"`
	LY  int            `json:"ly"`
	LZ  int            `json:"lz"`
	P1  int            `json:"p1,omitempty"`
	P2  int            `json:"p2,omitempty"`
	B   int            `json:"b,omitempty"`
	Msg string         `json:"msg,omitempty"`
	X   int            `json:"x,omitempty"`
	Y   int            `json:"y,omitempty"`
	Ex  map[string]any `json:"ex,omitempty"`
}

type Player struct {
	ID           int
	Loc          LocKey
	X, Y         int
	Alive        bool
	InBattle     int
	SlowUntil    time.Time
	SpawnProtect time.Time
	DeadUntil    time.Time
	HP           int
	Defending    bool
	Ink          int
	Gold         int
	LastPin      time.Time

	KnownLocations  map[LocKey]LocKnowLevel
	KnownPOI        map[string]POIKnow
	LastPOILoot     map[string]time.Time
	SeenPOI 	    map[string]time.Time
	SeenPOICount 	map[string]int
}

type Gate struct {
	FromX, FromY int
	ToLoc        LocKey
	ToX, ToY     int
	TwoWay       bool
	Tag          string
}

type Entrance struct {
	ID       int
	Kind     EntranceKind
	X, Y     int
	Hidden   bool
	PocketID int
}

type Location struct {
	Key       LocKey
	Kind      LocKind
	Walkable  [LocSize][LocSize]bool
	Gates     []Gate
	Entrances []*Entrance

	Players map[int]*Player
	Occ     map[[2]int][]int
}

type Battle struct {
	ID    int
	Loc   LocKey
	Cells map[[2]int]bool

	Phase Phase

	PositionRoundsRemain int
	Participants         map[int]int
	TurnOrder            []int
	TurnIndex            int
}

type World struct {
	Locations map[LocKey]*Location
	Battles   map[int]*Battle

	NextBattleID   int
	NextEntranceID int
	NextPocketID   int

	LastForgetSweep time.Time

	LogFile  *os.File
	Rng      *rand.Rand
	OnEvent  func(Event)
}
