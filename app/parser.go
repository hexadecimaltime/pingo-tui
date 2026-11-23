package app

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func parsePollHTML(body []byte) (PollData, bool) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return PollData{}, false
	}
	form := findPollForm(doc)
	if form == nil || form.Length() == 0 {
		return PollData{}, false
	}
	return parseForm(doc, form, body)
}

func parseSurveyHTML(body []byte) (PollData, bool) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return PollData{}, false
	}
	form := findPollForm(doc)
	if form == nil || form.Length() == 0 {
		return PollData{}, false
	}
	return parseForm(doc, form, body)
}

func findPollForm(doc *goquery.Document) *goquery.Selection {
	var best *goquery.Selection
	forms := doc.Find("form")
	forms.EachWithBreak(func(_ int, s *goquery.Selection) bool {
		action, _ := s.Attr("action")
		if strings.Contains(action, "/vote") && hasPollInputs(s) {
			best = s
			return false
		}
		return true
	})
	if best != nil {
		return best
	}
	forms.EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if hasPollInputs(s) {
			best = s
			return false
		}
		return true
	})
	return best
}

func hasPollInputs(form *goquery.Selection) bool {
	textInputs := form.Find("input[type='text'], input[type='number'], textarea")
	if textInputs.Length() > 0 {
		return true
	}
	optionInputs := form.Find("input[name='option'], input[name='option[]'], input[name='options[]'], input[name='survey_answer[]']")
	return optionInputs.Length() > 0
}

func extractSurveyID(hidden map[string]string, formAction string) string {
	keys := []string{"survey", "survey_id", "surveyid", "surveyId"}
	for _, key := range keys {
		if value, ok := hidden[key]; ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	if matches := surveyIDPattern.FindStringSubmatch(formAction); len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func extractQuestion(doc *goquery.Document, form *goquery.Selection) string {
	questionSelectors := "h1, h2, h3, h4, .lead, .question-text, #question-text, [data-question-text]"
	if form != nil && form.Length() > 0 {
		if question := firstText(form.PrevAllFiltered(questionSelectors)); question != "" {
			return question
		}
		var nestedQuestion string
		form.PrevAll().EachWithBreak(func(_ int, s *goquery.Selection) bool {
			nestedQuestion = firstText(s.Find(questionSelectors))
			return nestedQuestion == ""
		})
		if nestedQuestion != "" {
			return nestedQuestion
		}
		if question := firstText(form.Parent().Find(questionSelectors)); question != "" {
			return question
		}
	}

	selectors := []string{
		"div.question-text",
		".question-text",
		"#question-text",
		"[data-question-text]",
		"h1.lead",
		"h2.lead",
		"h3.lead",
		"h4.lead",
		".lead",
	}
	for _, selector := range selectors {
		if question := firstText(doc.Find(selector)); question != "" {
			return question
		}
	}
	return ""
}

func firstText(selection *goquery.Selection) string {
	var text string
	selection.EachWithBreak(func(_ int, s *goquery.Selection) bool {
		text = strings.Join(strings.Fields(s.Text()), " ")
		return text == ""
	})
	return text
}

func parseForm(doc *goquery.Document, form *goquery.Selection, body []byte) (PollData, bool) {
	question := extractQuestion(doc, form)
	formAction, _ := form.Attr("action")
	if formAction == "" {
		formAction = "/vote"
	}

	hidden := map[string]string{}
	form.Find("input[type='hidden']").Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		value, _ := s.Attr("value")
		if name != "" {
			hidden[name] = value
		}
	})

	surveyID := extractSurveyID(hidden, formAction)

	countdownEnd, countdownTotal, hasCountdown := parseCountdown(body)

	textInput := form.Find("input[type='text'], input[type='number']").First()
	textArea := form.Find("textarea").First()
	if textInput.Length() > 0 || textArea.Length() > 0 {
		target := textInput
		if textArea.Length() > 0 {
			target = textArea
		}
		inputName, _ := target.Attr("name")
		inputType, _ := target.Attr("type")
		id, _ := target.Attr("id")
		label := strings.TrimSpace(doc.Find(fmt.Sprintf("label[for='%s']", id)).First().Text())
		pageText := strings.ToLower(doc.Text())
		isMultiText := strings.HasSuffix(inputName, "[]") || strings.Contains(pageText, "multiple answers") || strings.Contains(pageText, "mehrere antworten")
		isNumberInput := inputType == "number" || strings.Contains(pageText, "please enter the number without a thousands delimiter")
		if label == "" {
			label = t(msgEnterYourAnswer)
		}
		labelLower := strings.ToLower(label)
		if isNumberInput && strings.Contains(labelLower, "please enter the number") {
			label = t(msgEnterNumber)
		}
		if isMultiText && !strings.HasSuffix(inputName, "[]") {
			inputName += "[]"
		}

		return PollData{
			Question:       question,
			FormAction:     formAction,
			Hidden:         hidden,
			InputName:      inputName,
			InputLabel:     label,
			SurveyID:       surveyID,
			IsNumberInput:  isNumberInput,
			IsMultiText:    isMultiText,
			HasCountdown:   hasCountdown,
			CountdownEnd:   countdownEnd,
			CountdownTotal: countdownTotal,
		}, true
	}

	optionInputs := form.Find("input[name='option'], input[name='option[]'], input[name='options[]'], input[name='survey_answer[]']")
	options := make([]Option, 0)
	isMultiChoice := false
	inputName := "option"
	optionInputs.Each(func(i int, s *goquery.Selection) {
		if typ, _ := s.Attr("type"); typ == "hidden" {
			return
		}
		if typ, _ := s.Attr("type"); typ == "checkbox" {
			isMultiChoice = true
		}
		if name, ok := s.Attr("name"); ok {
			inputName = name
		}
		id, _ := s.Attr("id")
		label := strings.TrimSpace(doc.Find(fmt.Sprintf("label[for='%s']", id)).First().Text())
		value, _ := s.Attr("value")
		if label == "" {
			label = tData(msgOptionDefault, map[string]any{"Index": i + 1})
		}
		options = append(options, Option{Label: label, Value: value})
	})

	if len(options) == 0 {
		return PollData{}, false
	}

	return PollData{
		Question:       question,
		FormAction:     formAction,
		Hidden:         hidden,
		InputName:      inputName,
		SurveyID:       surveyID,
		IsMultiChoice:  isMultiChoice,
		Options:        options,
		HasCountdown:   hasCountdown,
		CountdownEnd:   countdownEnd,
		CountdownTotal: countdownTotal,
	}, true
}

func findSurveyURL(body []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var surveyURL string
	doc.Find("a[href]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		href, _ := s.Attr("href")
		text := strings.ToLower(strings.TrimSpace(s.Text()))
		hrefLower := strings.ToLower(href)
		if (strings.Contains(hrefLower, "survey") && strings.Contains(hrefLower, "participate")) ||
			(strings.Contains(text, "survey") && strings.Contains(text, "participate")) {
			surveyURL = href
			return false
		}
		return true
	})
	if surveyURL == "" {
		return ""
	}
	if strings.HasPrefix(surveyURL, "http") {
		return surveyURL
	}
	return baseURL + surveyURL
}

func parseCountdown(body []byte) (time.Time, time.Duration, bool) {
	matches := countdownPattern.FindSubmatch(body)
	if len(matches) < 2 {
		return time.Time{}, 0, false
	}
	value := string(matches[1])
	parsed, err := time.ParseDuration(value + "s")
	if err != nil {
		return time.Time{}, 0, false
	}
	if parsed > 1000*time.Second {
		ms, err := time.ParseDuration(value + "ms")
		if err != nil {
			return time.Time{}, 0, false
		}
		return time.Now().Add(ms), ms, true
	}
	return time.Now().Add(parsed), parsed, true
}
