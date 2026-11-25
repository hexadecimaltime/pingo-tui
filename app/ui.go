package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/hexadecimaltime/pingo-tui/history"
	"github.com/hexadecimaltime/pingo-tui/table"
)

// Exported AppState for plugins
type AppState int

const (
	StateSessionInput AppState = iota
	StateConfigMenu
	StateLoading
	StateTextInput
	StateMultiTextInput
	StateSingleChoice
	StateMultiChoice
	StateSubmitting
	StateRenameSession
	StateHistorySessionSelect
	StateHistoryView
	StateHistoryEdit
	StateDone
	StateError
	StatePluginView // Added for custom plugin rendering
)

// Exported for Yaegi plugins
type Option struct {
	Label string
	Value string
}

// Exported for Yaegi plugins
type PollData struct {
	Question       string
	FormAction     string
	Hidden         map[string]string
	InputName      string
	InputLabel     string
	SurveyID       string
	IsNumberInput  bool
	IsMultiText    bool
	IsMultiChoice  bool
	Options        []Option
	CountdownEnd   time.Time
	CountdownTotal time.Duration
	HasCountdown   bool
}

type pollFoundMsg struct {
	data PollData
}

type pollErrorMsg struct {
	err error
}

type submitResultMsg struct {
	success bool
	status  int
	body    string
}

type tickMsg time.Time
type followUpMsg struct{}
type countdownUpdateMsg struct {
	end      time.Time
	duration time.Duration
}
type pollStoppedMsg struct{}
type fayeConnectedMsg struct {
	client *FayeClient
	events <-chan FayeEvent
}
type fayeEventMsg struct {
	event FayeEvent
}
type fayeErrorMsg struct {
	err error
}
type historyLoadedMsg []history.Session
type answersLoadedMsg []history.Answer

// Exported Model for Yaegi plugins
type Model struct {
	State                  AppState
	SessionCode            string
	Question               string
	SessionInput           textinput.Model
	RenameInput            textinput.Model
	AnswerInput            textinput.Model
	Options                []Option
	Cursor                 int
	Selected               map[int]bool
	Answers                []string
	Data                   PollData
	Spinner                spinner.Model
	Progress               progress.Model
	ErrMsg                 string
	StatusMsg              string
	LastCountdownFetch     time.Time
	WsActive               bool
	Faye                   *FayeClient
	FayeEvents             <-chan FayeEvent
	LastIteration          int
	LastTimestamp          time.Time
	SubscribedSurvey       string
	Width                  int
	Height                 int
	OriginY                int
	MaxViewHeight          int
	Store                  history.Store
	SessionHistory         []history.Session
	HistoryCursor          int
	Cfg                    *AppConfig
	ConfigForm             *huh.Form
	IsPromptingCorrect     bool
	HistoryAnswers         []history.Answer
	EditInput              textinput.Model
	SelectedHistorySession string

	// Table Models
	HistorySessionTable table.Model
	HistoryAnswersTable table.Model

	// Plugin Manager Reference
	Plugins *PluginManager
}

func InitialModel(flags cliFlags, cfg *AppConfig, store history.Store, plugins *PluginManager) Model {
	sessionInput := textinput.New()
	sessionInput.Placeholder = t(msgSessionCode)
	sessionInput.CharLimit = 64
	sessionInput.SetWidth(40)
	sessionInput.Focus()

	answerInput := textinput.New()
	answerInput.Placeholder = t(msgEnterYourAnswer)
	answerInput.CharLimit = 512
	answerInput.SetWidth(60)

	editInput := textinput.New()
	editInput.Placeholder = t(msgEnterYourAnswer)
	editInput.CharLimit = 512
	editInput.SetWidth(60)

	spin := spinner.New()
	spin.Spinner = spinner.Line

	prog := progress.New(
		progress.WithColors(mochaRed, mochaMauve),
		progress.WithoutPercentage(),
		progress.WithWidth(40),
	)
	prog.EmptyColor = mochaSurface1

	state := StateSessionInput
	var configForm *huh.Form

	if flags.openConfig {
		state = StateConfigMenu

		// Hook: Ask plugins for any config UI groups they want to inject
		groups := []*huh.Group{
			huh.NewGroup(
				huh.NewConfirm().
					Title("Enable Local History Tracking?").
					Description("Saves session codes and answers to your local disk.").
					Value(&cfg.HistoryEnabled),
				huh.NewSelect[string]().
					Title("Interface Language").
					Description("Sets the default language for the application.").
					Options(
						huh.NewOption("System Default", ""),
						huh.NewOption("English", "en"),
						huh.NewOption("Deutsch", "de"),
					).
					Value(&cfg.Language),
			),
		}

		if pluginGroups := plugins.GetConfigGroups(cfg); pluginGroups != nil {
			groups = append(groups, pluginGroups...)
		}

		configForm = huh.NewForm(groups...).WithShowHelp(true)
	} else if flags.sessionCode != "" {
		if !sessionCodePattern.MatchString(flags.sessionCode) {
			fmt.Println("Error: Session code must be exactly 6 digits.")
		}
		state = StateLoading
	}

	renameInput := textinput.New()
	renameInput.Placeholder = t(msgEnterSessionName)
	renameInput.CharLimit = 32
	renameInput.SetWidth(40)

	st := table.DefaultStyles()
	st.Selected = okStyle.Bold(true)
	st.Header = titleStyle.BorderBottom(true).BorderStyle(lipgloss.NormalBorder())

	sessionTable := table.New(
		table.WithColumns([]table.Column{
			{Title: "Code", Width: 8},
			{Title: "Name", Width: 20},
			{Title: "Date", Width: 18},
		}),
		table.WithFocused(true),
		table.WithHeight(10),
		table.WithStyles(st),
	)

	ansTable := table.New(
		table.WithColumns([]table.Column{
			{Title: "Date", Width: 12},
			{Title: "Question", Width: 40},
			{Title: "", Width: 0}, // Hidden: Given Answer
			{Title: "", Width: 0}, // Hidden: Correct Answer
		}),
		table.WithFocused(true),
		table.WithHeight(12),
		table.WithStyles(st),
		table.WithExpansionFunc(func(index int, row table.Row, width int) string {
			if len(row) < 4 {
				return ""
			}
			wrapWidth := width - 8
			if wrapWidth < 20 {
				wrapWidth = 20
			}
			wrapStyle := lipgloss.NewStyle().Width(wrapWidth)

			qLabel := tData(msgQuestionLabel, map[string]any{"Question": row[1]})
			youLabel := fmt.Sprintf("    %s: %s", t(msgYou), row[2])
			correctLabel := fmt.Sprintf("    %s: %s", t(msgCorrect), row[3])

			content := fmt.Sprintf("%s\n%s\n%s\n", qLabel, youLabel, correctLabel)
			return hintStyle.Render(wrapStyle.Render(content))
		}),
	)

	return Model{
		State:               state,
		SessionCode:         flags.sessionCode,
		SessionInput:        sessionInput,
		RenameInput:         renameInput,
		AnswerInput:         answerInput,
		EditInput:           editInput,
		Selected:            map[int]bool{},
		Spinner:             spin,
		Progress:            prog,
		Store:               store,
		HistoryCursor:       -1,
		Cfg:                 cfg,
		ConfigForm:          configForm,
		HistorySessionTable: sessionTable,
		HistoryAnswersTable: ansTable,
		Plugins:             plugins,
	}
}

func LoadHistoryCmd(store history.Store) tea.Cmd {
	return func() tea.Msg {
		sessions, err := store.ListSessions(context.Background())
		if err != nil {
			return nil
		}
		return historyLoadedMsg(sessions)
	}
}

func LoadAnswersCmd(store history.Store) tea.Cmd {
	return func() tea.Msg {
		answers, err := store.ListAnswers(context.Background())
		if err != nil {
			return pollErrorMsg{err: err}
		}
		return answersLoadedMsg(answers)
	}
}

func (m Model) Init() tea.Cmd {
	if m.State == StateConfigMenu {
		return tea.Batch(m.ConfigForm.Init(), tea.RequestCursorPosition)
	}
	if m.State == StateLoading {
		return tea.Batch(m.Spinner.Tick, fetchActivePoll(m.SessionCode), startFaye(m.SessionCode), tick(), LoadHistoryCmd(m.Store), tea.RequestCursorPosition)
	}
	return tea.Batch(m.Spinner.Tick, textinput.Blink, tick(), LoadHistoryCmd(m.Store), tea.RequestCursorPosition)
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func afterDelay(msg tea.Msg, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return msg
	})
}

func submitAndSave(m Model, answers []string) tea.Cmd {
	saveCmd := func() tea.Msg {
		var opts []string
		for _, opt := range m.Options {
			opts = append(opts, opt.Label)
		}

		qType := "text"
		if m.Data.IsMultiChoice {
			qType = "multi_choice"
		} else if len(m.Options) > 0 {
			qType = "single_choice"
		} else if m.Data.IsMultiText {
			qType = "multi_text"
		}

		var humanReadableAnswers []string
		for _, ans := range answers {
			labelFound := ans
			for _, opt := range m.Options {
				if opt.Value == ans {
					labelFound = opt.Label
					break
				}
			}
			humanReadableAnswers = append(humanReadableAnswers, labelFound)
		}

		givenAnswer := strings.Join(humanReadableAnswers, ", ")
		_ = m.Store.SaveAnswer(context.Background(), m.SessionCode, m.Question, givenAnswer, opts, qType)
		return nil
	}
	return tea.Batch(SubmitVote(m.Data, answers), saveCmd)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nextModel, cmd := m.update(msg)
	if nm, ok := nextModel.(Model); ok {
		nm.syncOriginY()
		return nm, cmd
	}
	return nextModel, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.CursorPositionMsg:
		m.OriginY = msg.Y
		return m, nil
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
	}

	if m.State == StateConfigMenu {
		form, cmd := m.ConfigForm.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.ConfigForm = f
		}

		if m.ConfigForm.State == huh.StateCompleted {
			_ = SaveConfig(*m.Cfg)
			_ = initI18n(m.Cfg.Language)

			if m.Cfg.HistoryEnabled {
				q, err := history.InitDB()
				if err == nil {
					m.Store = history.NewSQLiteStore(q)
				}
			} else {
				m.Store = &history.NoOpStore{}
			}

			m.State = StateSessionInput
			return m, LoadHistoryCmd(m.Store)
		}

		if m.ConfigForm.State == huh.StateAborted {
			return m, tea.Quit
		}

		return m, cmd
	}

	switch msg := msg.(type) {
	case historyLoadedMsg:
		m.SessionHistory = msg
		var rows []table.Row
		for _, s := range msg {
			timeStr := s.LastJoinedAt.Local().Format("02.01.2006 15:04")
			name := ""
			if s.Name.Valid {
				name = s.Name.String
			}
			rows = append(rows, table.Row{s.Code, name, timeStr})
		}
		m.HistorySessionTable.SetRows(rows)
		return m, nil

	case answersLoadedMsg:
		m.HistoryAnswers = msg
		if m.SelectedHistorySession != "" {
			var rows []table.Row
			for _, a := range m.HistoryAnswers {
				if a.SessionCode == m.SelectedHistorySession {
					timeStr := a.AnsweredAt.Local().Format("02.01. 15:04")
					correct := a.CorrectAnswer.String
					if correct == "" {
						correct = t(msgUnknownCorrectAnswer)
					}
					rows = append(rows, table.Row{timeStr, a.QuestionText, a.GivenAnswer, correct})
				}
			}
			m.HistoryAnswersTable.SetRows(rows)
		}
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{tick()}
		if m.Data.HasCountdown {
			remaining := time.Until(m.Data.CountdownEnd)
			if remaining <= 0 && (m.State == StateTextInput || m.State == StateMultiTextInput || m.State == StateSingleChoice || m.State == StateMultiChoice) {
				m.State = StateDone
				m.StatusMsg = t(msgTimeRanOut)
				return m, tea.Quit
			}
			if cmd := m.syncCountdownProgress(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if shouldRefreshCountdown(m) && time.Since(m.LastCountdownFetch) >= time.Second {
			m.LastCountdownFetch = time.Now()
			cmds = append(cmds, fetchCountdown(m.SessionCode))
		}
		return m, tea.Batch(cmds...)

	case progress.FrameMsg:
		var cmd tea.Cmd
		m.Progress, cmd = m.Progress.Update(msg)
		return m, cmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(msg)
		return m, cmd

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		barWidth := msg.Width - 25
		if barWidth < 20 {
			barWidth = 20
		} else if barWidth > 50 {
			barWidth = 50
		}
		m.Progress.SetWidth(barWidth)
		m.HistorySessionTable.SetWidth(msg.Width - 4)
		m.HistoryAnswersTable.SetWidth(msg.Width - 4)

		ansCols := m.HistoryAnswersTable.Columns()
		if len(ansCols) >= 2 {
			qWidth := msg.Width - ansCols[0].Width - 10
			if qWidth < 20 {
				qWidth = 20
			}
			ansCols[1].Width = qWidth
			m.HistoryAnswersTable.SetColumns(ansCols)
		}
		m.syncOriginY()
		return m, nil

	case pollFoundMsg:
		m.Data = msg.data
		m.Question = msg.data.Question
		m.Options = msg.data.Options
		m.Answers = nil
		m.Cursor = 0
		m.Selected = map[int]bool{}
		m.AnswerInput.SetValue("")
		if msg.data.InputLabel != "" {
			m.AnswerInput.Placeholder = msg.data.InputLabel
		}
		if msg.data.SurveyID != "" {
			m.SubscribedSurvey = msg.data.SurveyID
			_ = subscribeFayeSurvey(m.Faye, msg.data.SurveyID)
		}
		m.LastCountdownFetch = time.Now()
		m.SessionInput.Blur()

		saveSessionCmd := func() tea.Msg {
			_ = m.Store.UpsertSession(context.Background(), m.SessionCode)
			return nil
		}

		var progressCmd tea.Cmd
		if m.Data.HasCountdown {
			if m.Data.CountdownTotal <= 0 {
				m.Data.CountdownTotal = time.Until(m.Data.CountdownEnd)
			}
			progressCmd = m.syncCountdownProgress()
		}

		if msg.data.IsMultiChoice {
			m.AnswerInput.Blur()
			m.State = StateMultiChoice
			return m, tea.Batch(saveSessionCmd, progressCmd)
		}
		if len(msg.data.Options) > 0 {
			m.AnswerInput.Blur()
			m.State = StateSingleChoice
			return m, tea.Batch(saveSessionCmd, progressCmd)
		}
		if msg.data.IsMultiText {
			m.AnswerInput.Focus()
			m.State = StateMultiTextInput
			return m, tea.Batch(saveSessionCmd, progressCmd)
		}
		m.AnswerInput.Focus()
		m.State = StateTextInput
		return m, tea.Batch(saveSessionCmd, progressCmd)

	case pollErrorMsg:
		m.State = StateError
		m.ErrMsg = msg.err.Error()
		return m, nil

	case submitResultMsg:
		if msg.success {
			// Hook: Notify plugins of a successful submission!
			if m.Plugins != nil {
				m.Plugins.OnSubmitSuccess(&m)
			}

			if _, isNoOp := m.Store.(*history.NoOpStore); isNoOp {
				m.State = StateDone
				m.StatusMsg = t(msgSuccessSubmitted)
				return m, tea.Quit
			}

			m.IsPromptingCorrect = true
			m.Cursor = 0
			m.Selected = map[int]bool{}
			m.AnswerInput.SetValue("")
			m.Answers = nil

			if m.Data.IsMultiChoice {
				m.State = StateMultiChoice
			} else if len(m.Options) > 0 {
				m.State = StateSingleChoice
			} else if m.Data.IsMultiText {
				m.State = StateMultiTextInput
			} else {
				m.State = StateTextInput
				return m, textinput.Blink
			}
			return m, nil
		}
		m.State = StateError
		m.ErrMsg = tData(msgFailedSubmitStatus, map[string]any{"Status": msg.status})
		return m, tea.Quit

	case pollStoppedMsg:
		m.State = StateDone
		m.StatusMsg = t(msgQuestionClosed)
		return m, afterDelay(followUpMsg{}, time.Second)

	case fayeConnectedMsg:
		m.Faye = msg.client
		m.FayeEvents = msg.events
		m.WsActive = true
		return m, listenFaye(m.FayeEvents)

	case fayeEventMsg:
		var cmd tea.Cmd
		m, cmd = handleFayeEvent(m, msg.event)
		return m, tea.Batch(cmd, listenFaye(m.FayeEvents))

	case fayeErrorMsg:
		m.WsActive = false
		return m, nil

	case countdownUpdateMsg:
		cmd := m.applyCountdown(msg.end, msg.duration)
		return m, cmd

	case followUpMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		// Hook: Let plugins intercept keys globally first!
		if m.Plugins != nil {
			if handled, cmd := m.Plugins.HandleKey(&m, msg); handled {
				return m, cmd
			}
		}

		switch msg.String() {
		case "q":
			if m.State != StateTextInput && m.State != StateMultiTextInput && m.State != StateSessionInput && m.State != StateRenameSession && m.State != StateHistoryEdit {
				return m, tea.Quit
			}
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit

		case "esc":
			if next, cmd := navigateBack(m); next != nil {
				return next, cmd
			}
		}

		switch m.State {
		case StateHistorySessionSelect:
			switch msg.String() {
			case "enter":
				return openSelectedHistorySession(m)
			case "q":
				m.State = StateSessionInput
				return m, nil
			default:
				var cmd tea.Cmd
				m.HistorySessionTable, cmd = m.HistorySessionTable.Update(msg)
				return m, cmd
			}

		case StateHistoryView:
			switch msg.String() {
			case "enter":
				return openSelectedHistoryAnswer(m)
			case "q":
				m.State = StateHistorySessionSelect
				return m, nil
			default:
				var cmd tea.Cmd
				m.HistoryAnswersTable, cmd = m.HistoryAnswersTable.Update(msg)
				return m, cmd
			}

		case StateHistoryEdit:
			currentAnswers := sessionAnswers(m)
			if len(currentAnswers) == 0 {
				return m, nil
			}
			currentAnswerRecord := currentAnswers[m.HistoryAnswersTable.Cursor()]

			if currentAnswerRecord.QuestionType == "single_choice" || currentAnswerRecord.QuestionType == "multi_choice" {
				switch msg.String() {
				case "up", "k":
					if m.Cursor > 0 {
						m.Cursor--
					}
				case "down", "j":
					if m.Cursor < len(m.Options)-1 {
						m.Cursor++
					}
				case "space":
					if currentAnswerRecord.QuestionType == "multi_choice" {
						if m.Selected == nil {
							m.Selected = make(map[int]bool)
						}
						m.Selected[m.Cursor] = !m.Selected[m.Cursor]
					}
				case "enter":
					return submitHistoryEdit(m)
				case "q":
					m.State = StateHistoryView
				}
				return m, nil
			}

			var cmd tea.Cmd
			m.EditInput, cmd = m.EditInput.Update(msg)

			if msg.String() == "enter" {
				return submitHistoryEdit(m)
			}
			return m, cmd

		case StateSessionInput:
			switch msg.String() {
			case "v":
				m.State = StateHistorySessionSelect
				m.HistorySessionTable.SetCursor(0)
				return m, LoadAnswersCmd(m.Store)
			case "r":
				if len(m.SessionHistory) > 0 && m.HistoryCursor >= 0 && m.HistoryCursor < len(m.SessionHistory) {
					m.State = StateRenameSession
					m.RenameInput.Reset()
					if m.SessionHistory[m.HistoryCursor].Name.Valid {
						m.RenameInput.SetValue(m.SessionHistory[m.HistoryCursor].Name.String)
					}
					m.RenameInput.Focus()
					return m, textinput.Blink
				}
				return m, nil
			case "up", "k":
				moveSessionHistoryCursor(&m, -1)
				return m, nil
			case "down", "j":
				moveSessionHistoryCursor(&m, 1)
				return m, nil
			}

			keyStr := msg.String()
			runes := []rune(keyStr)
			if len(runes) == 1 {
				r := runes[0]
				if r < '0' || r > '9' {
					return m, nil
				}
			}

			var cmd tea.Cmd
			m.SessionInput.Focus()
			m.SessionInput, cmd = m.SessionInput.Update(msg)
			if msg.String() == "enter" {
				return submitSessionInput(m)
			}
			return m, cmd
		case StateTextInput:
			var cmd tea.Cmd
			m.AnswerInput.Focus()
			m.AnswerInput, cmd = m.AnswerInput.Update(msg)
			if msg.String() == "enter" {
				return submitTextInput(m)
			}
			return m, cmd
		case StateMultiTextInput:
			var cmd tea.Cmd
			m.AnswerInput.Focus()
			m.AnswerInput, cmd = m.AnswerInput.Update(msg)
			if msg.String() == "enter" {
				return submitMultiTextInput(m)
			}
			return m, cmd
		case StateSingleChoice:
			return updateSingleChoice(m, msg)
		case StateMultiChoice:
			return updateMultiChoice(m, msg)
		case StateRenameSession:
			var cmd tea.Cmd
			m.RenameInput.Focus()
			m.RenameInput, cmd = m.RenameInput.Update(msg)
			if msg.String() == "enter" {
				return submitRenameSession(m)
			}
			return m, cmd
		}
	case tea.MouseMsg:
		return handleMouse(m, msg)
	case tea.PasteMsg:
		var cmd tea.Cmd
		switch m.State {
		case StateSessionInput:
			m.SessionInput, cmd = m.SessionInput.Update(msg)
			return m, cmd
		case StateTextInput, StateMultiTextInput:
			m.AnswerInput, cmd = m.AnswerInput.Update(msg)
			return m, cmd
		case StateRenameSession:
			m.RenameInput, cmd = m.RenameInput.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	return m, nil
}

func updateSingleChoice(m Model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()
	switch key.Code {
	case tea.KeyUp, 'k':
		if m.Cursor > 0 {
			m.Cursor--
		}
	case tea.KeyDown, 'j':
		if m.Cursor < len(m.Options)-1 {
			m.Cursor++
		}
	case tea.KeySpace, tea.KeyEnter:
		return submitSingleChoice(m)
	}
	return m, nil
}

func updateMultiChoice(m Model, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()
	switch key.Code {
	case tea.KeyUp, 'k':
		if m.Cursor > 0 {
			m.Cursor--
		}
	case tea.KeyDown, 'j':
		if m.Cursor < len(m.Options)-1 {
			m.Cursor++
		}
	case tea.KeySpace:
		if len(m.Options) == 0 {
			return m, nil
		}
		m.Selected[m.Cursor] = !m.Selected[m.Cursor]
	case tea.KeyEnter:
		return submitMultiChoice(m)
	}
	return m, nil
}

func sessionAnswers(m Model) []history.Answer {
	var currentAnswers []history.Answer
	for _, a := range m.HistoryAnswers {
		if a.SessionCode == m.SelectedHistorySession {
			currentAnswers = append(currentAnswers, a)
		}
	}
	return currentAnswers
}

func navigateBack(m Model) (tea.Model, tea.Cmd) {
	switch m.State {
	case StateRenameSession:
		m.State = StateSessionInput
		m.RenameInput.Blur()
		m.SessionInput.Focus()
		return m, textinput.Blink
	case StateHistoryEdit:
		m.State = StateHistoryView
		m.EditInput.Blur()
		return m, nil
	case StateHistoryView:
		m.State = StateHistorySessionSelect
		m.HistorySessionTable.Focus()
		return m, nil
	case StateHistorySessionSelect:
		m.State = StateSessionInput
		m.SessionInput.Focus()
		return m, textinput.Blink
	}
	if m.IsPromptingCorrect {
		return m, tea.Quit
	}
	return nil, nil
}

func moveSessionHistoryCursor(m *Model, delta int) {
	if len(m.SessionHistory) == 0 {
		return
	}
	if delta < 0 {
		if m.HistoryCursor > 0 {
			m.HistoryCursor--
		} else if m.HistoryCursor == -1 {
			m.HistoryCursor = len(m.SessionHistory) - 1
		}
	} else if delta > 0 {
		if m.HistoryCursor < len(m.SessionHistory)-1 {
			m.HistoryCursor++
		}
	}
	if m.HistoryCursor >= 0 && m.HistoryCursor < len(m.SessionHistory) {
		m.SessionInput.SetValue(m.SessionHistory[m.HistoryCursor].Code)
		m.SessionInput.SetCursor(len(m.SessionInput.Value()))
	}
}

func submitSessionInput(m Model) (tea.Model, tea.Cmd) {
	code := strings.TrimSpace(m.SessionInput.Value())
	if code != "" {
		if !sessionCodePattern.MatchString(code) {
			m.ErrMsg = t(msgSessionCodeInvalid)
			return m, nil
		}
		m.SessionCode = code
		m.State = StateLoading
		return m, tea.Batch(m.Spinner.Tick, fetchActivePoll(m.SessionCode), startFaye(m.SessionCode))
	}
	return m, nil
}

func submitTextInput(m Model) (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.AnswerInput.Value())

	if m.IsPromptingCorrect {
		if value != "" {
			_ = m.Store.UpdateCorrectAnswer(context.Background(), m.SessionCode, value)
		}
		return m, tea.Quit
	}

	if m.Data.IsNumberInput && !numberPattern.MatchString(value) {
		m.ErrMsg = t(msgNumberRequired)
		return m, nil
	}
	m.ErrMsg = ""
	m.State = StateSubmitting
	return m, submitAndSave(m, []string{value})
}

func submitMultiTextInput(m Model) (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.AnswerInput.Value())
	if value == "" {
		if m.IsPromptingCorrect {
			if len(m.Answers) > 0 {
				_ = m.Store.UpdateCorrectAnswer(context.Background(), m.SessionCode, strings.Join(m.Answers, ", "))
			}
			return m, tea.Quit
		}
		m.State = StateSubmitting
		return m, submitAndSave(m, m.Answers)
	}
	m.Answers = append(m.Answers, value)
	m.AnswerInput.SetValue("")
	return m, nil
}

func submitSingleChoice(m Model) (tea.Model, tea.Cmd) {
	if len(m.Options) == 0 || m.Cursor < 0 || m.Cursor >= len(m.Options) {
		return m, nil
	}
	choice := m.Options[m.Cursor]

	if m.IsPromptingCorrect {
		_ = m.Store.UpdateCorrectAnswer(context.Background(), m.SessionCode, choice.Label)
		return m, tea.Quit
	}

	m.State = StateSubmitting
	return m, submitAndSave(m, []string{choice.Value})
}

func submitMultiChoice(m Model) (tea.Model, tea.Cmd) {
	var values []string
	var labels []string
	for idx, opt := range m.Options {
		if m.Selected[idx] {
			values = append(values, opt.Value)
			labels = append(labels, opt.Label)
		}
	}
	if m.IsPromptingCorrect {
		if len(labels) > 0 {
			_ = m.Store.UpdateCorrectAnswer(context.Background(), m.SessionCode, strings.Join(labels, ", "))
		}
		return m, tea.Quit
	}
	m.State = StateSubmitting
	return m, submitAndSave(m, values)
}

func submitRenameSession(m Model) (tea.Model, tea.Cmd) {
	newName := strings.TrimSpace(m.RenameInput.Value())
	if m.HistoryCursor >= 0 && m.HistoryCursor < len(m.SessionHistory) {
		code := m.SessionHistory[m.HistoryCursor].Code
		_ = m.Store.UpdateSessionName(context.Background(), code, newName)
	}
	m.State = StateSessionInput
	m.RenameInput.Blur()
	m.SessionInput.Focus()
	return m, tea.Batch(LoadHistoryCmd(m.Store), textinput.Blink)
}

func openSelectedHistorySession(m Model) (tea.Model, tea.Cmd) {
	cursor := m.HistorySessionTable.Cursor()
	if len(m.SessionHistory) > 0 && cursor >= 0 && cursor < len(m.SessionHistory) {
		m.SelectedHistorySession = m.SessionHistory[cursor].Code
		m.State = StateHistoryView

		var rows []table.Row
		for _, a := range m.HistoryAnswers {
			if a.SessionCode == m.SelectedHistorySession {
				timeStr := a.AnsweredAt.Local().Format("02.01. 15:04")
				correct := a.CorrectAnswer.String
				if correct == "" {
					correct = t(msgUnknownCorrectAnswer)
				}
				rows = append(rows, table.Row{timeStr, a.QuestionText, a.GivenAnswer, correct})
			}
		}
		m.HistoryAnswersTable.SetRows(rows)
		m.HistoryAnswersTable.SetCursor(0)
	}
	return m, nil
}

func openSelectedHistoryAnswer(m Model) (tea.Model, tea.Cmd) {
	currentAnswers := sessionAnswers(m)
	cursor := m.HistoryAnswersTable.Cursor()
	if len(currentAnswers) > 0 && cursor >= 0 && cursor < len(currentAnswers) {
		m.State = StateHistoryEdit
		a := currentAnswers[cursor]
		currentAns := a.CorrectAnswer.String

		var opts []string
		if a.Options != "" {
			_ = json.Unmarshal([]byte(a.Options), &opts)
		}

		m.Options = nil
		for _, opt := range opts {
			m.Options = append(m.Options, Option{Label: opt, Value: opt})
		}

		if a.QuestionType == "single_choice" || a.QuestionType == "multi_choice" {
			m.Cursor = 0
			if a.QuestionType == "multi_choice" {
				m.Selected = make(map[int]bool)
			} else {
				m.Selected = nil
			}
			return m, nil
		}

		m.EditInput.SetValue(currentAns)
		m.EditInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func submitHistoryEdit(m Model) (tea.Model, tea.Cmd) {
	currentAnswers := sessionAnswers(m)
	cursor := m.HistoryAnswersTable.Cursor()
	if len(currentAnswers) == 0 || cursor < 0 || cursor >= len(currentAnswers) {
		return m, nil
	}
	currentAnswerRecord := currentAnswers[cursor]
	id := currentAnswerRecord.ID

	if currentAnswerRecord.QuestionType == "single_choice" || currentAnswerRecord.QuestionType == "multi_choice" {
		var selectedLabels []string
		if currentAnswerRecord.QuestionType == "single_choice" {
			if len(m.Options) > 0 && m.Cursor >= 0 && m.Cursor < len(m.Options) {
				selectedLabels = append(selectedLabels, m.Options[m.Cursor].Label)
			}
		} else {
			for idx, opt := range m.Options {
				if m.Selected[idx] {
					selectedLabels = append(selectedLabels, opt.Label)
				}
			}
		}
		newAns := strings.Join(selectedLabels, ", ")
		_ = m.Store.UpdateCorrectAnswerByID(context.Background(), id, newAns)
		m.State = StateHistoryView
		return m, LoadAnswersCmd(m.Store)
	}

	newAns := strings.TrimSpace(m.EditInput.Value())
	_ = m.Store.UpdateCorrectAnswerByID(context.Background(), id, newAns)
	m.State = StateHistoryView
	m.EditInput.Blur()
	return m, LoadAnswersCmd(m.Store)
}

func handleMouse(m Model, msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.Plugins != nil {
		if handled, cmd := m.Plugins.HandleMouse(&m, msg); handled {
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		switch m.State {
		case StateSessionInput:
			switch msg.Button {
			case tea.MouseWheelUp:
				moveSessionHistoryCursor(&m, -1)
			case tea.MouseWheelDown:
				moveSessionHistoryCursor(&m, 1)
			}
			return m, nil
		case StateSingleChoice, StateMultiChoice:
			switch msg.Button {
			case tea.MouseWheelUp:
				if m.Cursor > 0 {
					m.Cursor--
				}
			case tea.MouseWheelDown:
				if m.Cursor < len(m.Options)-1 {
					m.Cursor++
				}
			}
			return m, nil
		case StateHistorySessionSelect:
			var cmd tea.Cmd
			m.HistorySessionTable, cmd = m.HistorySessionTable.Update(msg)
			return m, cmd
		case StateHistoryView:
			var cmd tea.Cmd
			m.HistoryAnswersTable, cmd = m.HistoryAnswersTable.Update(msg)
			return m, cmd
		case StateHistoryEdit:
			currentAnswers := sessionAnswers(m)
			cursor := m.HistoryAnswersTable.Cursor()
			if len(currentAnswers) > 0 && cursor >= 0 && cursor < len(currentAnswers) {
				a := currentAnswers[cursor]
				if a.QuestionType == "single_choice" || a.QuestionType == "multi_choice" {
					switch msg.Button {
					case tea.MouseWheelUp:
						if m.Cursor > 0 {
							m.Cursor--
						}
					case tea.MouseWheelDown:
						if m.Cursor < len(m.Options)-1 {
							m.Cursor++
						}
					}
				}
			}
			return m, nil
		}
		return m, nil

	case tea.MouseClickMsg:
		m.syncOriginY()
		relY := msg.Y - m.OriginY
		switch msg.Button {
		case tea.MouseRight, tea.MouseBackward:
			if next, cmd := navigateBack(m); next != nil {
				return next, cmd
			}
			return m, nil
		case tea.MouseLeft:
			if fy := footerY(m); fy >= 0 && relY == fy {
				if action := footerActionAtX(m, msg.X); action != "" {
					return handleFooterAction(m, action)
				}
				return m, nil
			}
			return handleLeftClick(m, msg.X, relY)
		}
	}

	return m, nil
}

func handleFooterAction(m Model, action string) (tea.Model, tea.Cmd) {
	switch action {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		if next, cmd := navigateBack(m); next != nil {
			return next, cmd
		}
		return m, nil
	case "v":
		if m.State == StateSessionInput {
			m.State = StateHistorySessionSelect
			m.HistorySessionTable.SetCursor(0)
			return m, LoadAnswersCmd(m.Store)
		}
	case "space":
		switch m.State {
		case StateMultiChoice:
			if len(m.Options) > 0 && m.Cursor >= 0 && m.Cursor < len(m.Options) {
				if m.Selected == nil {
					m.Selected = make(map[int]bool)
				}
				m.Selected[m.Cursor] = !m.Selected[m.Cursor]
			}
		case StateHistoryEdit:
			currentAnswers := sessionAnswers(m)
			cursor := m.HistoryAnswersTable.Cursor()
			if len(currentAnswers) > 0 && cursor >= 0 && cursor < len(currentAnswers) && currentAnswers[cursor].QuestionType == "multi_choice" {
				if len(m.Options) > 0 && m.Cursor >= 0 && m.Cursor < len(m.Options) {
					if m.Selected == nil {
						m.Selected = make(map[int]bool)
					}
					m.Selected[m.Cursor] = !m.Selected[m.Cursor]
				}
			}
		}
	case "enter":
		switch m.State {
		case StateSessionInput:
			return submitSessionInput(m)
		case StateTextInput:
			return submitTextInput(m)
		case StateMultiTextInput:
			return submitMultiTextInput(m)
		case StateSingleChoice:
			return submitSingleChoice(m)
		case StateMultiChoice:
			return submitMultiChoice(m)
		case StateRenameSession:
			return submitRenameSession(m)
		case StateHistorySessionSelect:
			return openSelectedHistorySession(m)
		case StateHistoryView:
			return openSelectedHistoryAnswer(m)
		case StateHistoryEdit:
			return submitHistoryEdit(m)
		}
	}
	return m, nil
}

func handleLeftClick(m Model, _, y int) (tea.Model, tea.Cmd) {
	switch m.State {
	case StateSessionInput:
		if y == 2 {
			m.SessionInput.Focus()
			return m, textinput.Blink
		}
		if len(m.SessionHistory) > 0 {
			idx := y - 5
			if idx >= 0 && idx < len(m.SessionHistory) {
				if m.HistoryCursor == idx {
					m.SessionInput.SetValue(m.SessionHistory[idx].Code)
					m.SessionInput.SetCursor(len(m.SessionInput.Value()))
					return submitSessionInput(m)
				}
				m.HistoryCursor = idx
				m.SessionInput.SetValue(m.SessionHistory[idx].Code)
				m.SessionInput.SetCursor(len(m.SessionInput.Value()))
			}
		}
		return m, nil

	case StateSingleChoice:
		optStartY := choiceOptionsStartY(m)
		if idx := y - optStartY; idx >= 0 && idx < len(m.Options) {
			m.Cursor = idx
			return submitSingleChoice(m)
		}
		return m, nil

	case StateMultiChoice:
		optStartY := choiceOptionsStartY(m)
		if idx := y - optStartY; idx >= 0 && idx < len(m.Options) {
			m.Cursor = idx
			if m.Selected == nil {
				m.Selected = make(map[int]bool)
			}
			m.Selected[idx] = !m.Selected[idx]
			return m, nil
		}
		if y == optStartY+len(m.Options) {
			return submitMultiChoice(m)
		}
		return m, nil

	case StateTextInput:
		m.AnswerInput.Focus()
		return m, textinput.Blink

	case StateMultiTextInput:
		if y == multiTextSubmitHintY(m) {
			return submitMultiTextInput(m)
		}
		m.AnswerInput.Focus()
		return m, textinput.Blink

	case StateRenameSession:
		m.RenameInput.Focus()
		return m, textinput.Blink

	case StateHistorySessionSelect:
		if len(m.SessionHistory) > 0 {
			rowIdx := m.HistorySessionTable.RowAtY(y - 2)
			if rowIdx >= 0 && rowIdx < len(m.SessionHistory) {
				m.HistorySessionTable.SetCursor(rowIdx)
				return openSelectedHistorySession(m)
			}
		}
		return m, nil

	case StateHistoryView:
		rowIdx := m.HistoryAnswersTable.RowAtY(y - 2)
		if rowIdx >= 0 {
			if rowIdx != m.HistoryAnswersTable.Cursor() {
				m.HistoryAnswersTable.SetCursor(rowIdx)
				return m, nil
			}
			return openSelectedHistoryAnswer(m)
		}
		return m, nil

	case StateHistoryEdit:
		currentAnswers := sessionAnswers(m)
		cursor := m.HistoryAnswersTable.Cursor()
		if len(currentAnswers) == 0 || cursor < 0 || cursor >= len(currentAnswers) {
			return m, nil
		}
		a := currentAnswers[cursor]
		if a.QuestionType == "single_choice" || a.QuestionType == "multi_choice" {
			optStartY := historyEditOptionsStartY(m, a)
			if idx := y - optStartY; idx >= 0 && idx < len(m.Options) {
				m.Cursor = idx
				if a.QuestionType == "single_choice" {
					return submitHistoryEdit(m)
				}
				if m.Selected == nil {
					m.Selected = make(map[int]bool)
				}
				m.Selected[idx] = !m.Selected[idx]
				return m, nil
			}
			if a.QuestionType == "multi_choice" && y == optStartY+len(m.Options)+1 {
				return submitHistoryEdit(m)
			}
			return m, nil
		}
		m.EditInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func shouldRefreshCountdown(m Model) bool {
	return !m.WsActive && m.SessionCode != "" && (m.State == StateTextInput || m.State == StateMultiTextInput || m.State == StateSingleChoice || m.State == StateMultiChoice)
}

func (m *Model) applyCountdown(end time.Time, dur time.Duration) tea.Cmd {
	if dur <= 0 {
		dur = time.Until(end)
	}
	if dur < 0 {
		dur = 0
	}
	if !m.Data.HasCountdown || m.Data.CountdownTotal <= 0 || dur > m.Data.CountdownTotal {
		m.Data.CountdownTotal = dur
	}
	m.Data.CountdownEnd = end
	m.Data.HasCountdown = true
	return m.syncCountdownProgress()
}

func (m *Model) syncCountdownProgress() tea.Cmd {
	if !m.Data.HasCountdown || m.Data.CountdownTotal <= 0 {
		return nil
	}
	remaining := time.Until(m.Data.CountdownEnd)
	if remaining < 0 {
		remaining = 0
	}
	percent := float64(remaining) / float64(m.Data.CountdownTotal)
	if percent < 0 {
		percent = 0
	} else if percent > 1 {
		percent = 1
	}
	return m.Progress.SetPercent(percent)
}

func handleFayeEvent(m Model, event FayeEvent) (Model, tea.Cmd) {
	data := event.Data
	switch data.Type {
	case "status_change":
		if !timestampOk(&m, data.Timestamp) {
			return m, nil
		}
		if data.Session != "" && m.SubscribedSurvey != data.Session {
			m.SubscribedSurvey = data.Session
			_ = subscribeFayeSurvey(m.Faye, data.Session)
		}
		switch payloadString(data.Payload) {
		case "stop_scheduled":
			if data.Time > 0 {
				dur := time.Duration(data.Time) * time.Millisecond
				cmd := m.applyCountdown(time.Now().Add(dur), dur)
				return m, cmd
			}
		case "stopped":
			m.State = StateDone
			m.StatusMsg = t(msgQuestionClosed)
		}
	case "countdown":
		if !iterationOk(&m, data.Iteration) {
			return m, nil
		}
		if payload := payloadFloat(data.Payload); payload > 0 {
			dur := time.Duration(payload) * time.Millisecond
			cmd := m.applyCountdown(time.Now().Add(dur), dur)
			return m, cmd
		}
	}
	return m, nil
}

func timestampOk(m *Model, ts string) bool {
	if ts == "" {
		return true
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return true
	}
	if m.LastTimestamp.IsZero() || parsed.After(m.LastTimestamp) {
		m.LastTimestamp = parsed
		return true
	}
	return false
}

func iterationOk(m *Model, iteration int) bool {
	if iteration == 0 {
		return true
	}
	if iteration == 1 || iteration > m.LastIteration {
		m.LastIteration = iteration
		return true
	}
	return false
}

func payloadString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return ""
}

func payloadFloat(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return 0
}

func correctPromptLine(m Model) string {
	if m.IsPromptingCorrect {
		return okStyle.Render("✓ Successfully submitted!") + "\n" +
			hintStyle.Render("What was the correct answer? (Saves to local history, Esc to skip)") + "\n"
	}
	return ""
}

func newInteractiveView(content string) tea.View {
	v := tea.NewView(content)
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func viewLineCount(m Model) int {
	content := strings.TrimRight(m.View().Content, "\n")
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

func (m *Model) syncOriginY() {
	h := viewLineCount(*m)
	if h > m.MaxViewHeight {
		m.MaxViewHeight = h
	}
	if m.Height > 0 && m.MaxViewHeight > 0 && m.OriginY+m.MaxViewHeight > m.Height {
		m.OriginY = max(0, m.Height-m.MaxViewHeight)
	}
}

func blockStartY(targetIdx int, lines ...string) int {
	start := 0
	for start < targetIdx && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if targetIdx <= start {
		return 0
	}
	return strings.Count(strings.Join(lines[start:targetIdx], "\n"), "\n") + 1
}

func choiceOptionsStartY(m Model) int {
	q := questionLine(m.Question)
	lines := []string{timeLeftLine(m)}
	if p := correctPromptLine(m); p != "" {
		lines = append(lines, p)
	}
	if q != "" {
		lines = append(lines, q)
	}
	lines = append(lines, "")
	targetIdx := len(lines)
	lines = append(lines, choiceListView(m.Options, m.Cursor, nil))
	return blockStartY(targetIdx, lines...)
}

func historyEditOptionsStartY(m Model, a history.Answer) int {
	inputView := choiceListView(m.Options, m.Cursor, m.Selected)
	return blockStartY(
		5,
		titleStyle.Render(t(msgEditCorrectAnswerTitle)),
		"",
		hintStyle.Render(tData(msgQuestionLabel, map[string]any{"Question": a.QuestionText})),
		hintStyle.Render(tData(msgYourAnswerLabel, map[string]any{"Answer": a.GivenAnswer})),
		"",
		inputView,
	)
}

func multiTextSubmitHintY(m Model) int {
	answers := strings.Join(m.Answers, ", ")
	q := questionLine(m.Question)
	lines := []string{timeLeftLine(m)}
	if p := correctPromptLine(m); p != "" {
		lines = append(lines, p)
	}
	if q != "" {
		lines = append(lines, q)
	}
	lines = append(lines,
		"",
		hintStyle.Render(tData(msgAnswersSoFar, map[string]any{"Answers": answers})),
		m.AnswerInput.View(),
		errLine(m.ErrMsg),
	)
	targetIdx := len(lines)
	lines = append(lines, hintStyle.Render(t(msgEnterToAdd)))
	return blockStartY(targetIdx, lines...)
}

func footerY(m Model) int {
	if m.State == StateDone || m.State == StateError || m.State == StateConfigMenu {
		return -1
	}
	content := strings.TrimRight(m.View().Content, "\n")
	if content == "" {
		return -1
	}
	return strings.Count(content, "\n")
}

func (m Model) View() tea.View {
	switch m.State {
	case StatePluginView:
		// Hook: Render custom plugin views
		return newInteractiveView(joinLines(
			titleStyle.Render("PLUGIN VIEW"),
			m.Plugins.RenderView(&m),
			footerLine(m),
		))
	case StateConfigMenu:
		return newInteractiveView(joinLines(
			titleStyle.Render("PINGO CONFIGURATION"),
			"",
			m.ConfigForm.View(),
		))

	case StateSessionInput:
		historyView := ""
		if len(m.SessionHistory) > 0 {
			var b strings.Builder
			b.WriteString(hintStyle.Render(t(msgRecentSessions)))
			b.WriteString("\n")
			for i, s := range m.SessionHistory {
				cursorMark := " "
				if i == m.HistoryCursor {
					cursorMark = ">"
				}
				timeStr := s.LastJoinedAt.Local().Format("02.01.2006 15:04")

				displayStr := s.Code
				if s.Name.Valid && s.Name.String != "" {
					displayStr = fmt.Sprintf("%s [%s]", s.Name.String, s.Code)
				}
				b.WriteString(fmt.Sprintf("%s %s (%s)\n", cursorMark, displayStr, timeStr))
			}
			historyView = "\n" + strings.TrimRight(b.String(), "\n")
		}

		return newInteractiveView(joinLines(
			titleStyle.Render("PINGO"),
			"",
			m.SessionInput.View(),
			historyView,
			errLine(m.ErrMsg),
			"",
			footerLine(m),
		))

	case StateRenameSession:
		return newInteractiveView(joinLines(
			titleStyle.Render(t(msgRenameSessionTitle)),
			"",
			hintStyle.Render(tData(msgNewNameForSession, map[string]any{"Code": m.SessionHistory[m.HistoryCursor].Code})),
			m.RenameInput.View(),
			"",
			footerLine(m),
		))

	case StateLoading:
		return newInteractiveView(joinLines(
			logStyle.Render(t(msgConnecting)),
			logStyle.Render(fmt.Sprintf("%s %s", m.Spinner.View(), t(msgWaitingActive))),
			"",
			footerLine(m),
		))

	case StateTextInput:
		q := questionLine(m.Question)
		lines := []string{timeLeftLine(m)}
		if p := correctPromptLine(m); p != "" {
			lines = append(lines, p)
		}
		if q != "" {
			lines = append(lines, q)
		}
		lines = append(lines, "", m.AnswerInput.View(), errLine(m.ErrMsg), "", footerLine(m))
		return newInteractiveView(joinLines(lines...))

	case StateMultiTextInput:
		answers := strings.Join(m.Answers, ", ")
		q := questionLine(m.Question)
		lines := []string{timeLeftLine(m)}
		if p := correctPromptLine(m); p != "" {
			lines = append(lines, p)
		}
		if q != "" {
			lines = append(lines, q)
		}
		lines = append(lines,
			"",
			hintStyle.Render(tData(msgAnswersSoFar, map[string]any{"Answers": answers})),
			m.AnswerInput.View(),
			errLine(m.ErrMsg),
			hintStyle.Render(t(msgEnterToAdd)),
			"",
			footerLine(m),
		)
		return newInteractiveView(joinLines(lines...))

	case StateSingleChoice:
		q := questionLine(m.Question)
		lines := []string{timeLeftLine(m)}
		if p := correctPromptLine(m); p != "" {
			lines = append(lines, p)
		}
		if q != "" {
			lines = append(lines, q)
		}
		lines = append(lines, "", choiceListView(m.Options, m.Cursor, nil), "", footerLine(m))
		return newInteractiveView(joinLines(lines...))

	case StateMultiChoice:
		q := questionLine(m.Question)
		lines := []string{timeLeftLine(m)}
		if p := correctPromptLine(m); p != "" {
			lines = append(lines, p)
		}
		if q != "" {
			lines = append(lines, q)
		}
		lines = append(lines,
			"",
			choiceListView(m.Options, m.Cursor, m.Selected),
			hintStyle.Render(t(msgSpaceToggleEnterSubmit)),
			"",
			footerLine(m),
		)
		return newInteractiveView(joinLines(lines...))

	case StateSubmitting:
		q := questionLine(m.Question)
		lines := []string{timeLeftLine(m)}
		if q != "" {
			lines = append(lines, q)
		}
		lines = append(lines,
			"",
			hintStyle.Render(fmt.Sprintf("%s %s", m.Spinner.View(), t(msgSubmitting))),
			"",
			footerLine(m),
		)
		return newInteractiveView(joinLines(lines...))

	case StateHistorySessionSelect:
		var b strings.Builder
		b.WriteString(titleStyle.Render(t(msgHistoryViewTitle)))
		b.WriteString("\n\n")

		if len(m.SessionHistory) == 0 {
			b.WriteString(hintStyle.Render(t(msgNoAnswers)))
			b.WriteString("\n")
		} else {
			b.WriteString(m.HistorySessionTable.View())
			b.WriteString("\n")
		}

		b.WriteString("\n")
		b.WriteString(footerLine(m))
		return newInteractiveView(b.String())

	case StateHistoryView:
		currentAnswers := sessionAnswers(m)

		var b strings.Builder
		b.WriteString(titleStyle.Render(fmt.Sprintf("%s [%s]", t(msgHistoryViewTitle), m.SelectedHistorySession)))
		b.WriteString("\n\n")

		if len(currentAnswers) == 0 {
			b.WriteString(hintStyle.Render(t(msgNoAnswers)))
			b.WriteString("\n")
		} else {
			b.WriteString(m.HistoryAnswersTable.View())
			b.WriteString("\n")
		}

		b.WriteString("\n")
		b.WriteString(footerLine(m))
		return newInteractiveView(b.String())

	case StateHistoryEdit:
		currentAnswers := sessionAnswers(m)

		var a history.Answer
		if len(currentAnswers) > 0 {
			a = currentAnswers[m.HistoryAnswersTable.Cursor()]
		}

		var inputView string
		if a.QuestionType == "single_choice" || a.QuestionType == "multi_choice" {
			inputView = choiceListView(m.Options, m.Cursor, m.Selected)
			if a.QuestionType == "multi_choice" {
				inputView += "\n\n" + hintStyle.Render(t(msgSpaceToggleEnterSubmit))
			}
		} else {
			inputView = m.EditInput.View()
		}

		return newInteractiveView(joinLines(
			titleStyle.Render(t(msgEditCorrectAnswerTitle)),
			"",
			hintStyle.Render(tData(msgQuestionLabel, map[string]any{"Question": a.QuestionText})),
			hintStyle.Render(tData(msgYourAnswerLabel, map[string]any{"Answer": a.GivenAnswer})),
			"",
			inputView,
			"",
			footerLine(m),
		))

	case StateDone:
		return tea.NewView(joinLines(okStyle.Render(m.StatusMsg)))

	case StateError:
		return tea.NewView(joinLines(errStyle.Render(m.ErrMsg)))
	default:
		return tea.NewView("")
	}
}

func timeLeftLine(m Model) string {
	if !m.Data.HasCountdown {
		return ""
	}
	remaining := time.Until(m.Data.CountdownEnd)
	if remaining < 0 {
		remaining = 0
	}
	label := hintStyle.Render(t(msgTimeLeft))
	value := lipgloss.NewStyle().Bold(true).Foreground(mochaMauve).Render(formatDuration(remaining))
	return label + value + "  " + m.Progress.View()
}

func questionLine(question string) string {
	if strings.TrimSpace(question) == "" {
		return ""
	}
	return questionStyle.Render(t(msgQuestionPrefix) + highlightMath(question))
}

func errLine(msg string) string {
	if msg == "" {
		return ""
	}
	return errStyle.Render(msg)
}

func choiceListView(options []Option, cursor int, selected map[int]bool) string {
	var b strings.Builder
	for i, opt := range options {
		cursorMark := " "
		if i == cursor {
			cursorMark = ">"
		}
		label := highlightMath(opt.Label)
		if selected != nil {
			mark := " "
			if selected[i] {
				mark = "x"
			}
			fmt.Fprintf(&b, "%s [%s] %s\n", cursorMark, mark, label)
		} else {
			fmt.Fprintf(&b, "%s %s\n", cursorMark, label)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatDuration(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 0 {
		seconds = 0
	}
	minutes := seconds / 60
	secs := seconds % 60
	return fmt.Sprintf("%02d:%02d", minutes, secs)
}

func joinLines(lines ...string) string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		filtered = append(filtered, line)
	}
	start := 0
	end := len(filtered)
	for start < end && strings.TrimSpace(filtered[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(filtered[end-1]) == "" {
		end--
	}
	return strings.Join(filtered[start:end], "\n") + "\n"
}

func footerLine(m Model) string {
	if m.State == StateDone || m.State == StateError || m.State == StateConfigMenu {
		return ""
	}
	width := m.Width
	if width <= 0 {
		width = 80
	}
	left := footerHotkeys(m)
	right := keyStyle.Render("Ctrl+C") + barDimStyle.Render(" "+t(msgQuit))
	separator := barSepStyle.Render("|")
	gap := barDimStyle.Render("  ")
	content := left + gap + separator + gap + right
	innerWidth := width - 2
	if innerWidth < 10 {
		innerWidth = width
	}
	contentWidth := lipgloss.Width(content)
	if contentWidth < innerWidth {
		content += barDimStyle.Render(strings.Repeat(" ", innerWidth-contentWidth))
	}
	return footerStyle.Width(width).Padding(0, 1).Render(content)
}

type footerItem struct {
	rendered string
	action   string
}

func footerHotkeyItems(m Model) []footerItem {
	var items []footerItem

	if m.IsPromptingCorrect {
		items = []footerItem{
			{rendered: keyStyle.Render("Enter") + barDimStyle.Render(" "+t(msgSubmit)), action: "enter"},
			{rendered: keyStyle.Render("Esc") + barDimStyle.Render(" "+t(msgBack)), action: "esc"},
		}
	} else if m.State == StateHistoryView || m.State == StateHistorySessionSelect {
		items = []footerItem{
			{rendered: keyStyle.Render("Up/Down") + barDimStyle.Render(" "+t(msgMove)), action: ""},
			{rendered: keyStyle.Render("Enter") + barDimStyle.Render(" "+t(msgEdit)), action: "enter"},
			{rendered: keyStyle.Render("Esc") + barDimStyle.Render(" "+t(msgBack)), action: "esc"},
		}
	} else if m.State == StateHistoryEdit {
		currentAnswers := sessionAnswers(m)
		cursor := m.HistoryAnswersTable.Cursor()
		if len(currentAnswers) > 0 && cursor >= 0 && cursor < len(currentAnswers) {
			a := currentAnswers[cursor]
			switch a.QuestionType {
			case "multi_choice":
				items = []footerItem{
					{rendered: keyStyle.Render("Up/Down") + barDimStyle.Render(" "+t(msgMove)), action: ""},
					{rendered: keyStyle.Render("Space") + barDimStyle.Render(" "+t(msgToggle)), action: "space"},
					{rendered: keyStyle.Render("Enter") + barDimStyle.Render(" "+t(msgSubmit)), action: "enter"},
					{rendered: keyStyle.Render("Esc") + barDimStyle.Render(" "+t(msgBack)), action: "esc"},
				}
			case "single_choice":
				items = []footerItem{
					{rendered: keyStyle.Render("Up/Down") + barDimStyle.Render(" "+t(msgMove)), action: ""},
					{rendered: keyStyle.Render("Enter") + barDimStyle.Render(" "+t(msgSubmit)), action: "enter"},
					{rendered: keyStyle.Render("Esc") + barDimStyle.Render(" "+t(msgBack)), action: "esc"},
				}
			default:
				items = []footerItem{
					{rendered: keyStyle.Render("Enter") + barDimStyle.Render(" "+t(msgSubmit)), action: "enter"},
					{rendered: keyStyle.Render("Esc") + barDimStyle.Render(" "+t(msgBack)), action: "esc"},
				}
			}
		} else {
			items = []footerItem{
				{rendered: keyStyle.Render("Enter") + barDimStyle.Render(" "+t(msgSubmit)), action: "enter"},
				{rendered: keyStyle.Render("Esc") + barDimStyle.Render(" "+t(msgBack)), action: "esc"},
			}
		}
	} else {
		if m.State == StateSessionInput {
			items = append(items, footerItem{rendered: keyStyle.Render("v") + barDimStyle.Render(" "+t(msgViewHistory)), action: "v"})
		}
		if m.State == StateSingleChoice || m.State == StateMultiChoice {
			items = append(items, footerItem{rendered: keyStyle.Render("Up/Down") + barDimStyle.Render(" "+t(msgMove)), action: ""})
		}
		if m.State == StateMultiChoice {
			items = append(items, footerItem{rendered: keyStyle.Render("Space") + barDimStyle.Render(" "+t(msgToggle)), action: "space"})
		}
		items = append(items, footerItem{rendered: keyStyle.Render("Enter") + barDimStyle.Render(" "+t(msgSubmit)), action: "enter"})
	}

	return items
}

func footerActionAtX(m Model, clickX int) string {
	if clickX < 1 {
		return ""
	}
	items := footerHotkeyItems(m)
	right := footerItem{
		rendered: keyStyle.Render("Ctrl+C") + barDimStyle.Render(" "+t(msgQuit)),
		action:   "ctrl+c",
	}
	allItems := append(items, right)

	x := 1
	for _, item := range allItems {
		w := lipgloss.Width(item.rendered)
		if clickX >= x && clickX < x+w {
			return item.action
		}
		x += w + 5
	}
	return ""
}

func footerHotkeys(m Model) string {
	items := footerHotkeyItems(m)
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.rendered)
	}

	joiner := barDimStyle.Render("  ") + barSepStyle.Render("|") + barDimStyle.Render("  ")
	return strings.Join(parts, joiner)
}
