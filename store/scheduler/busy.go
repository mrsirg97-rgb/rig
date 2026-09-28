package scheduler

import (
	"context"
	"encoding/json"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type busyResult struct {
	kind   string
	names  string
	reason string
}

func busyState(fetch Fetch, swapURL, jobModel string) busyResult {
	modelsRaw, err := fetch(swapURL + "/v1/models")
	if err != nil {
		return busyResult{kind: "error", reason: "busy check failed: " + err.Error()}
	}
	runningRaw, err := fetch(swapURL + "/running")
	if err != nil {
		return busyResult{kind: "error", reason: "busy check failed: " + err.Error()}
	}

	var models struct {
		Data []struct {
			ID   string `json:"id"`
			Meta struct {
				LLamaSwap struct {
					Aliases []string `json:"aliases"`
				} `json:"llamaswap"`
			} `json:"meta"`
			Status struct {
				Value string `json:"value"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(modelsRaw, &models); err != nil {
		return busyResult{kind: "error", reason: "busy check failed: models: " + err.Error()}
	}
	var running struct {
		Running []struct {
			Model string `json:"model"`
		} `json:"running"`
	}
	if err := json.Unmarshal(runningRaw, &running); err != nil {
		return busyResult{kind: "error", reason: "busy check failed: running: " + err.Error()}
	}

	canon := map[string]string{}
	for _, m := range models.Data {
		for _, n := range append([]string{m.ID}, m.Meta.LLamaSwap.Aliases...) {
			canon[n] = m.ID
		}
	}
	norm := func(n string) string {
		if c, ok := canon[n]; ok {
			return c
		}
		return n
	}
	own := norm(jobModel)
	for _, m := range models.Data {
		if norm(m.ID) == own && m.Status.Value == "loaded" {
			return busyResult{kind: "run"}
		}
	}
	resident := map[string]bool{}
	for _, r := range running.Running {
		resident[norm(r.Model)] = true
	}
	if resident[own] {
		return busyResult{kind: "run"}
	}
	if len(resident) == 0 {
		return busyResult{kind: "run"}
	}
	var names []string
	for n := range resident {
		names = append(names, n)
	}
	sort.Strings(names)
	return busyResult{kind: "busy", names: strings.Join(names, ", ")}
}

func jobSpent(db DB, id string) (float64, error) {
	var spent float64
	err := db.DB.QueryRow(`SELECT COALESCE(SUM(cost), 0) FROM runs WHERE job_id = ?`, id).Scan(&spent)
	if err != nil {
		return 0, err
	}
	return spent, nil
}

func workerSessionCost(home, cwd, session string) float64 {
	if home == "" || session == "" {
		return 0
	}
	path := state.StorePath(home, cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0
	}
	db, _, _, err := store.Open(path, state.Statements(), state.SchemaVersion, state.Migration())
	if err != nil {
		return 0
	}
	defer db.DB.Close()
	cost, err := state.SessionCost(context.Background(), db, session)
	if err != nil {
		return 0
	}
	return cost
}
