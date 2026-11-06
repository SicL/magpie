// Package orchestration adds an opt-in Projects & Tasks page to Magpie.
// The execution scheduler is intentionally not part of the model gateway.
package orchestration

import "github.com/yetone/magpie/internal/pluginrt"

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "orchestration", API: "^1", Requires: []string{"window"},
		Name: "Projects", Sub: "Coordinate projects and tasks across coding agents",
		Parts:  []pluginrt.Part{{Slot: "nav.page", Key: "orchestration"}},
		Client: []string{"i18n.js", "orchestration.css", "client.js"},
	})
}
