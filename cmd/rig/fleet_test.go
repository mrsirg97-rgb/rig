package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/models"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

func swapFetch(running []string, slots int) sched.Fetch {
	return func(url string) (json.RawMessage, error) {
		switch {
		case strings.HasSuffix(url, "/v1/models"):
			status := "unloaded"
			for _, m := range running {
				if m == "local" {
					status = "loaded"
				}
			}
			return json.RawMessage(`{"data":[{"id":"local","status":{"value":"` + status + `"}}]}`), nil
		case strings.HasSuffix(url, "/running"):
			type row struct {
				Model string `json:"model"`
			}
			var out []row
			for _, m := range running {
				out = append(out, row{Model: m})
			}
			b, _ := json.Marshal(map[string]any{"running": out})
			return b, nil
		case strings.Contains(url, "/upstream/"):
			type slot struct {
				ID           int  `json:"id"`
				IsProcessing bool `json:"is_processing"`
			}
			var out []slot
			for i := 0; i < slots; i++ {
				out = append(out, slot{ID: i})
			}
			b, _ := json.Marshal(out)
			return b, nil
		}
		return nil, fmt.Errorf("unexpected url %s", url)
	}
}

func localTable(t *testing.T) models.Table {
	t.Helper()
	tbl, err := models.New(models.Model{ID: "local", Window: 8192, MaxTokens: 1024, Reserve: 64, KeepRecent: 128, Role: models.RoleInteractive})
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func remoteTable(t *testing.T) models.Table {
	t.Helper()
	tbl, err := models.New(models.Model{ID: "brain", Window: 8192, MaxTokens: 1024, Reserve: 64, KeepRecent: 128, Role: models.RoleWorker, Remote: true, BaseURL: "https://endpoint.example/v1"})
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func TestFleetWiringOneSlotKeepsTheDrainPairOff(t *testing.T) {
	on, why := fleetWiring(swapFetch([]string{"local"}, 1), "http://127.0.0.1:8090", "local", localTable(t), true)
	if on {
		t.Fatal("a one-slot server cannot host a second request: the drain pair stays off")
	}
	want := "swarm: the fleet needs more than one slot (the resident server runs one, and a turn holds it)"
	if why != want {
		t.Fatalf("the swarm refusal = %q, want %q", why, want)
	}
}

func TestFleetWiringTwoSlotsWiresTheDrainPair(t *testing.T) {
	on, why := fleetWiring(swapFetch([]string{"local"}, 2), "http://127.0.0.1:8090", "local", localTable(t), true)
	if !on {
		t.Fatalf("a two-slot server hosts the fleet, refusal %q", why)
	}
	if why != "" {
		t.Fatalf("a wired fleet carries no refusal, got %q", why)
	}
}

func TestFleetWiringRemoteRowNeedsNoSlots(t *testing.T) {
	failing := func(url string) (json.RawMessage, error) {
		return nil, fmt.Errorf("the swap must never be consulted: %s", url)
	}
	on, why := fleetWiring(failing, "http://127.0.0.1:8090", "brain", remoteTable(t), true)
	if !on {
		t.Fatalf("a remote row runs its own parallelism, refusal %q", why)
	}
}

func TestFleetWiringWorkersFalseTurnsThePairOffOnACapableMachine(t *testing.T) {
	on, why := fleetWiring(swapFetch([]string{"local"}, 2), "http://127.0.0.1:8090", "local", localTable(t), false)
	if on {
		t.Fatal("workers:false turns the drain pair off even at two slots")
	}
	want := `swarm: the worker tools are off (settings.json "workers": false)`
	if why != want {
		t.Fatalf("the swarm refusal = %q, want %q", why, want)
	}
}

func TestFleetWiringNothingResidentIsNotCapability(t *testing.T) {
	on, why := fleetWiring(swapFetch(nil, 2), "http://127.0.0.1:8090", "local", localTable(t), true)
	if on {
		t.Fatal("nothing resident at start is not capability: the pair stays off")
	}
	want := "swarm: the fleet needs more than one slot (nothing resident at start)"
	if why != want {
		t.Fatalf("the swarm refusal = %q, want %q", why, want)
	}
}

func TestFleetWiringUnreadableSwapFailsClosed(t *testing.T) {
	failing := func(url string) (json.RawMessage, error) {
		return nil, fmt.Errorf("connection refused: %s", url)
	}
	on, why := fleetWiring(failing, "http://127.0.0.1:8090", "local", localTable(t), true)
	if on {
		t.Fatal("an unreadable swap is not capability: the pair stays off")
	}
	want := "swarm: the fleet needs more than one slot (the swap is unreadable at start)"
	if why != want {
		t.Fatalf("the swarm refusal = %q, want %q", why, want)
	}
}

func TestSwarmAdapterRefusesWithTheWireReason(t *testing.T) {
	want := "swarm: the fleet needs more than one slot (the resident server runs one, and a turn holds it)"
	a := swarmAdapter{c: nil, why: want}
	_, err := a.Start(context.Background(), command.SwarmStart{Role: "worker"})
	if err == nil || err.Error() != want {
		t.Fatalf("the refusal = %v, want %q", err, want)
	}
	if rows := a.List(); rows != nil {
		t.Fatalf("an unwired swarm lists nothing, got %v", rows)
	}
}
