package app

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

type PluginManager struct {
	i *interp.Interpreter
}

// Accept the symbols from the outside!
func NewPluginManager(symbols interp.Exports) *PluginManager {
	i := interp.New(interp.Options{})
	i.Use(stdlib.Symbols)
	i.Use(symbols)
	return &PluginManager{i: i}
}

func (pm *PluginManager) LoadPlugins(dir string) {
	files, err := os.ReadDir(dir)
	if err != nil || len(files) == 0 {
		return
	}
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".go" {
			_, err := pm.i.EvalPath(filepath.Join(dir, f.Name()))
			if err != nil {
				fmt.Printf("Error loading plugin %s: %v\n", f.Name(), err)
			}
		}
	}
}

// Hook: Intercept keystrokes globally
func (pm *PluginManager) HandleKey(m *Model, msg tea.KeyMsg) (bool, tea.Cmd) {
	v, err := pm.i.Eval("HandleKey")
	if err == nil && v.IsValid() {
		if fn, ok := v.Interface().(func(*Model, tea.KeyMsg) (bool, tea.Cmd)); ok {
			return fn(m, msg)
		}
	}
	return false, nil
}

// Hook: Triggered immediately after a poll answer is successfully submitted
func (pm *PluginManager) OnSubmitSuccess(m *Model) {
	v, err := pm.i.Eval("OnSubmitSuccess")
	if err == nil && v.IsValid() {
		if fn, ok := v.Interface().(func(*Model)); ok {
			fn(m)
		}
	}
}

// Hook: Allows plugins to inject native UI menus into the configuration screen
func (pm *PluginManager) GetConfigGroups(cfg *AppConfig) []*huh.Group {
	v, err := pm.i.Eval("GetConfigGroups")
	if err == nil && v.IsValid() {
		if fn, ok := v.Interface().(func(*AppConfig) []*huh.Group); ok {
			return fn(cfg)
		}
	}
	return nil
}

// Hook: Renders a custom UI view when the app is in StatePluginView
func (pm *PluginManager) RenderView(m *Model) string {
	v, err := pm.i.Eval("RenderView")
	if err == nil && v.IsValid() {
		if fn, ok := v.Interface().(func(*Model) string); ok {
			return fn(m)
		}
	}
	return "Plugin Error: RenderView function not found or invalid signature."
}
