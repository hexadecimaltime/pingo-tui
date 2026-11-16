package app

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/hexadecimaltime/pingo-tui/history"
	"github.com/traefik/yaegi/interp"
)

var version = "dev"

// Start initializes and runs the Pingo Bubble Tea application.
func Start(args []string, symbols interp.Exports) error {
	flags, err := parseArgs(args)
	if err != nil {
		return err
	}

	cfg := LoadConfig()

	activeLang := cfg.Language
	if flags.langFlag != "" {
		activeLang = flags.langFlag
	}

	if err := initI18n(activeLang); err != nil {
		return err
	}

	if flags.historyOn {
		cfg.HistoryEnabled = true
		if err := SaveConfig(cfg); err != nil {
			return fmt.Errorf("error saving config: %w", err)
		}
		fmt.Println("History tracking is now permanently ENABLED.")
		return nil
	}

	if flags.historyOff {
		cfg.HistoryEnabled = false
		if err := SaveConfig(cfg); err != nil {
			return fmt.Errorf("error saving config: %w", err)
		}
		fmt.Println("History tracking is now permanently DISABLED.")
		return nil
	}

	if flags.clearHistory {
		var confirm bool
		err := huh.NewConfirm().
			Title(t(msgConfirmClearHistory)).
			Affirmative(t(msgConfirmYes)).
			Negative(t(msgConfirmNo)).
			Value(&confirm).
			Run()

		if err != nil {
			return err
		}

		if !confirm {
			fmt.Println(t(msgOperationCancelled))
			return nil
		}

		if err := history.ClearDB(); err != nil {
			return fmt.Errorf("%s", tData(msgFailedClearDB, map[string]any{"Err": err.Error()}))
		}
		fmt.Println(t(msgHistoryCleared))
		return nil
	}

	if flags.showHelp {
		fmt.Print(buildUsage())
		return nil
	}

	if flags.showVersion {
		fmt.Println(version)
		return nil
	}

	enableHistory := cfg.HistoryEnabled || flags.historyTemp

	var store history.Store
	if enableHistory {
		q, err := history.InitDB()
		if err != nil {
			return fmt.Errorf("failed to initialize history database: %w", err)
		}
		store = history.NewSQLiteStore(q)
	} else {
		store = &history.NoOpStore{}
	}

	// 1. Initialize Plugin Engine
	pluginManager := NewPluginManager(symbols)

	// 2. Load `.go` scripts from local folder
	pluginDir := "./plugins"
	_ = os.MkdirAll(pluginDir, 0755) // Ensure directory exists
	pluginManager.LoadPlugins(pluginDir)

	// 3. Inject plugins into the initial model
	p := tea.NewProgram(InitialModel(flags, &cfg, store, pluginManager))

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("%s %w", t(msgErrorPrefix), err)
	}
	return nil
}
