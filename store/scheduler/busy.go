package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
	"os"
	"path/filepath"
)

func canonicalModels(fetch Fetch, swapURL string) (map[string]string, []string, error) {
	modelsRaw, err := fetch(swapURL + "/v1/models")
	if err != nil {
		return nil, nil, fmt.Errorf("gate check failed: %v", err)
	}
	runningRaw, err := fetch(swapURL + "/running")
	if err != nil {
		return nil, nil, fmt.Errorf("gate check failed: %v", err)
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
		return nil, nil, fmt.Errorf("gate check failed: models: %v", err)
	}
	var running struct {
		Running []struct {
			Model string `json:"model"`
		} `json:"running"`
	}
	if err := json.Unmarshal(runningRaw, &running); err != nil {
		return nil, nil, fmt.Errorf("gate check failed: running: %v", err)
	}

	canon := map[string]string{}
	for _, m := range models.Data {
		for _, n := range append([]string{m.ID}, m.Meta.LLamaSwap.Aliases...) {
			canon[n] = m.ID
		}
	}
	var resident []string
	seen := map[string]bool{}
	add := func(n string) {
		if c, ok := canon[n]; ok {
			n = c
		}
		if !seen[n] {
			seen[n] = true
			resident = append(resident, n)
		}
	}
	for _, m := range models.Data {
		if m.Status.Value == "loaded" {
			add(m.ID)
		}
	}
	for _, r := range running.Running {
		add(r.Model)
	}
	sort.Strings(resident)
	return canon, resident, nil
}

func FreeSlots(fetch Fetch, swapURL, model string) (int, int, error) {
	slots, err := slotRead(fetch, swapURL, model)
	if err != nil {
		return 0, 0, err
	}
	return slots.free, slots.total, nil
}

type noRowError struct {
	resident string
	known    string
}

func (e noRowError) Error() string {
	return fmt.Sprintf("no model row for the resident %q (known: %s)", e.resident, e.known)
}

func resolveResidentModel(fetch Fetch, swapURL string, table models.Table) (string, string, error) {
	canon, resident, err := canonicalModels(fetch, swapURL)
	if err != nil {
		return "", "", err
	}
	if len(resident) == 0 {
		return "", "", nil
	}
	id := resident[0]
	if _, ok := table.Get(id); ok {
		return id, id, nil
	}
	for _, alias := range aliasNames(canon, id) {
		if _, ok := table.Get(alias); ok {
			return alias, id, nil
		}
	}
	return "", "", noRowError{resident: id, known: strings.Join(table.Known(), ", ")}
}

func aliasNames(canon map[string]string, id string) []string {
	var out []string
	for name, c := range canon {
		if name != id && c == id {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func ResidentModel(fetch Fetch, swapURL string) (string, error) {
	_, resident, err := canonicalModels(fetch, swapURL)
	if err != nil {
		return "", err
	}
	if len(resident) == 0 {
		return "", nil
	}
	return resident[0], nil
}

func FleetCapacity(fetch Fetch, swapURL string) (int, error) {
	_, resident, err := canonicalModels(fetch, swapURL)
	if err != nil {
		return 0, err
	}
	widest := 0
	for _, model := range resident {
		_, total, err := FreeSlots(fetch, swapURL, model)
		if err != nil {
			return 0, err
		}
		if total > widest {
			widest = total
		}
	}
	return widest, nil
}

type slotSet struct {
	free  int
	total int
}

func slotRead(fetch Fetch, swapURL, model string) (slotSet, error) {
	raw, err := fetch(swapURL + "/upstream/" + url.PathEscape(model) + "/slots")
	if err != nil {
		return slotSet{}, fmt.Errorf("gate check failed: slots: %v", err)
	}
	var slots []struct {
		IsProcessing bool `json:"is_processing"`
	}
	if err := json.Unmarshal(raw, &slots); err != nil {
		return slotSet{}, fmt.Errorf("gate check failed: slots: %v", err)
	}
	out := slotSet{total: len(slots)}
	for _, s := range slots {
		if !s.IsProcessing {
			out.free++
		}
	}
	return out, nil
}

var ErrNotResident = errors.New("a different model is resident")

func holderRefusal(resident []string) error {
	return fmt.Errorf("%w: the GPU is held by %s (the fleet is the resident model; eviction is the operator's act)", ErrNotResident, strings.Join(resident, ", "))
}

func gateOnce(fetch Fetch, swapURL, model string) error {
	canon, resident, err := canonicalModels(fetch, swapURL)
	if err != nil {
		return err
	}
	if len(resident) == 0 {
		return nil
	}
	own := model
	if c, ok := canon[model]; ok {
		own = c
	}
	if !contains(resident, own) {
		return fmt.Errorf("%w; run on the resident model or schedule a once-job — it fires between turns", holderRefusal(resident))
	}
	return nil
}

func contains(all []string, one string) bool {
	for _, s := range all {
		if s == one {
			return true
		}
	}
	return false
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
