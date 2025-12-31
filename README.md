# Kingdom Intrigue MVP — Web (v4)

This build runs the world as a server and provides a small browser UI.

## Run
```powershell
go run . -port 8080 -players 220 -tickms 150
```

Open:
- http://localhost:8080/

API:
- /api/world
- /api/player?id=1
- /api/loc?x=2&y=2&z=0
- /api/events?tail=200
- /api/stream  (SSE; emits `world` events)

Notes:
- Static UI is served from `./web`.
- World is updated in a tick loop in a goroutine.

# Kingdom Intrigue (MVP)

Realtime online world simulation prototype for a kingdom of intrigue, power, honor, and survival.

**Core ideas**
- Shared world map split into locations (cells).
- Location = 100x100 tiles.
- Online movement; when players collide, a turn-based battle starts.
- Nearby players may be slowed/affected by the battle.
- World has an information layer: **players vs knowledge vs experienced**.

## Run

### Requirements
- Go 1.21+ (recommended)

### Start
```powershell
go run . -port 8080 -players 500 -tickms 150