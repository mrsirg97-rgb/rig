package todo

import (
	"fmt"
	"strconv"

	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
)

func itemsOf(tasks []map[string]any) ([]todostore.CreateItem, error) {
	if tasks == nil {
		return nil, fmt.Errorf("action 'create' requires tasks: array of {text}")
	}
	var items []todostore.CreateItem
	for _, raw := range tasks {
		var item todostore.CreateItem
		text, ok := raw["text"].(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("todo: tasks[].text required")
		}
		item.Text = text
		for _, link := range []struct {
			key string
			set func(*todostore.CreateItem, bool)
			ptr func(*todostore.CreateItem, *string)
		}{
			{"requires", func(it *todostore.CreateItem, v bool) { it.RequiresNull = v }, func(it *todostore.CreateItem, v *string) { it.Requires = v }},
			{"blocks", func(it *todostore.CreateItem, v bool) { it.BlocksNull = v }, func(it *todostore.CreateItem, v *string) { it.Blocks = v }},
		} {
			if v, present := raw[link.key]; present {
				switch dep := v.(type) {
				case nil:
					link.set(&item, true)
				case string:
					if dep != "" {
						link.ptr(&item, &dep)
					}
				case float64:
					if dep != float64(int64(dep)) || dep < 1 {
						return nil, fmt.Errorf("todo: tasks[].%s: %v is not a position in this list (1 is the first)", link.key, dep)
					}
					pos := strconv.FormatInt(int64(dep), 10)
					link.ptr(&item, &pos)
				default:
					return nil, fmt.Errorf("todo: tasks[].%s must be a sibling's number in this list, a task id (tN), its exact text, or null", link.key)
				}
			}
		}
		items = append(items, item)
	}
	return items, nil
}
