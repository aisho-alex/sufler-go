package analyzer

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"sufler-go/internal/config"
)

const askPrompt = "Ты — ассистент-суфлёр на живом разговоре. Ниже — последние реплики " +
	"разговора и вопрос пользователя. Ответь на вопрос кратко (до 3 " +
	"предложений), опираясь на контекст разговора. Если ответа в контексте " +
	"нет — так и скажи и предложи, что уточнить у собеседника. " +
	"Отвечай обычным текстом, без JSON и без markdown."

const userTag = "[пользователь]"

var hintSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"hint":        map[string]any{"type": []string{"string", "null"}},
		"importance":  map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
		"note":        map[string]any{"type": []string{"string", "null"}},
		"note_reason": map[string]any{"type": []string{"string", "null"}},
	},
	"required": []string{"hint", "importance", "note"},
}

type Hint struct {
	Hint       string
	Note       string
	Importance string
	Ts         float64
	Model      string
}

type entry struct {
	ts    float64
	src   string
	text  string
	words int
}

type cmdKind int

const (
	cmdTranscript cmdKind = iota
	cmdAsk
)

type cmd struct {
	kind cmdKind
	text string
}

type Analyzer struct {
	Cfg *config.LLM

	SystemPrompt string
	OnHint       func(Hint)
	OnAnswer     func(question, answer string)
	OnStatus     func(string)
	Log          func(string)

	client *http.Client
	q      chan cmd
	window []entry
	mu     sync.Mutex
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once

	lastRequest   float64
	lastAnalysis  float64
	newWords      int
	backoff       float64
	guidedEnabled bool
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func New(cfg *config.LLM, systemPrompt string) *Analyzer {
	return &Analyzer{
		Cfg:          cfg,
		SystemPrompt: systemPrompt,
		client: &http.Client{
			Timeout: time.Duration(cfg.TimeoutS * float64(time.Second)),
			Transport: &http.Transport{
				ForceAttemptHTTP2: false,
				TLSClientConfig: &tls.Config{
					NextProtos: []string{"http/1.1"},
				},
			},
		},
		q:             make(chan cmd, 256),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		guidedEnabled: cfg.GuidedJSON,
	}
}

func (a *Analyzer) logf(format string, args ...any) {
	if a.Log != nil {
		a.Log("[llm] " + fmt.Sprintf(format, args...))
	}
}

func (a *Analyzer) status(s string) {
	if a.OnStatus != nil {
		func() {
			defer recover()
			a.OnStatus(s)
		}()
	}
}

func (a *Analyzer) Start() {
	go a.loop()
	a.logf("%s @ %s", a.Cfg.Model, a.Cfg.BaseURL)
}

func (a *Analyzer) AddTranscript(source, text string) {
	a.remember(source, text)
	select {
	case a.q <- cmd{kind: cmdTranscript}:
	default:
	}
}

func (a *Analyzer) Ask(question string) {
	question = strings.TrimSpace(question)
	if question == "" {
		return
	}
	a.remember("user", question)
	select {
	case a.q <- cmd{kind: cmdAsk, text: question}:
	default:
		a.logf("очередь переполнена, вопрос пропущен")
	}
}

func (a *Analyzer) AddUserNote(text string) {
	text = strings.TrimSpace(text)
	if text != "" {
		a.remember("user", "заметка: "+text)
	}
}

func (a *Analyzer) remember(source, text string) {
	words := len(strings.Fields(text))
	a.mu.Lock()
	a.window = append(a.window, entry{ts: now(), src: source, text: text, words: words})
	if source != "user" {
		a.newWords += words
	}
	a.mu.Unlock()
	a.prune()
}

func (a *Analyzer) prune() {
	cutoff := now() - float64(a.Cfg.ContextSeconds)*3
	a.mu.Lock()
	i := 0
	for i < len(a.window) && a.window[i].ts < cutoff {
		i++
	}
	if i > 0 {
		a.window = append([]entry{}, a.window[i:]...)
	}
	a.mu.Unlock()
}

func (a *Analyzer) loop() {
	defer close(a.done)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-a.stop:
			return
		case c := <-a.q:
			if c.kind == cmdAsk {
				a.handleAsk(c.text)
			} else {
				a.maybeAnalyze()
			}
		case <-tick.C:
			a.maybeAnalyze()
		}
	}
}

func (a *Analyzer) handleAsk(question string) {
	a.status("отвечаю…")
	context := a.buildContext()
	messages := []chatMessage{
		{Role: "system", Content: askPrompt},
		{Role: "user", Content: fmt.Sprintf(
			"Контекст разговора:\n%s\n\nВопрос пользователя: %s",
			context, question)},
	}
	answer, err := a.chat(a.Cfg.Model, messages, a.Cfg.Temperature, 400, nil)
	if err != nil {
		a.logf("ошибка ответа на вопрос: %v", err)
	}
	a.status(map[bool]string{true: "ответ готов", false: "нет ответа"}[answer != ""])
	if a.OnAnswer != nil {
		a.OnAnswer(question, answer)
	}
}

func (a *Analyzer) maybeAnalyze() {
	a.prune()
	a.mu.Lock()
	hasNew := a.newWords > 0
	a.mu.Unlock()
	if !hasNew {
		return
	}
	n := now()
	if n-a.lastRequest < a.Cfg.MinIntervalS+a.backoff {
		return
	}
	byTime := n-a.lastAnalysis >= a.Cfg.TriggerSeconds
	a.mu.Lock()
	byWords := a.newWords >= a.Cfg.TriggerWords
	a.mu.Unlock()
	if !byTime && !byWords {
		return
	}
	context := a.buildContext()
	if context == "" {
		return
	}
	a.lastRequest = n
	a.status("анализирую…")
	hint := a.request(context)
	if hint != nil {
		a.lastAnalysis = n
		a.mu.Lock()
		a.newWords = 0
		a.mu.Unlock()
		a.backoff = 0
		a.status("подсказка готова")
		if a.OnHint != nil {
			a.OnHint(*hint)
		}
	} else {
		a.status("без подсказок")
	}
}

func (a *Analyzer) buildContext() string {
	cutoff := now() - float64(a.Cfg.ContextSeconds)
	a.mu.Lock()
	var b strings.Builder
	for _, e := range a.window {
		if e.ts < cutoff {
			continue
		}
		b.WriteString(tag(e.src))
		b.WriteString(" ")
		b.WriteString(e.text)
		b.WriteString("\n")
	}
	a.mu.Unlock()
	ctx := strings.TrimRight(b.String(), "\n")
	r := []rune(ctx)
	if len(r) > a.Cfg.MaxContextChars {
		ctx = string(r[len(r)-a.Cfg.MaxContextChars:])
	}
	return ctx
}

func tag(src string) string {
	switch src {
	case "mic":
		return "[я]"
	case "user":
		return userTag
	}
	return "[собеседник]"
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
	GuidedJSON  any           `json:"guided_json,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("http %d: %s", e.Status, e.Body)
}

func (a *Analyzer) chat(model string, messages []chatMessage, temperature float64,
	maxTokens int, guided any) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model: model, Messages: messages,
		Temperature: temperature, MaxTokens: maxTokens, GuidedJSON: guided,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST",
		strings.TrimRight(a.Cfg.BaseURL, "/")+"/chat/completions",
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.Cfg.APIKey)
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", &apiError{Status: resp.StatusCode, Body: buf.String()}
	}
	var out chatResponse
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", errors.New("пустой ответ")
	}
	return out.Choices[0].Message.Content, nil
}

func (a *Analyzer) request(context string) *Hint {
	base := []chatMessage{
		{Role: "system", Content: a.SystemPrompt},
		{Role: "user", Content: context},
	}
	type attempt struct {
		guided bool
	}
	attempts := []attempt{{guided: false}}
	if a.guidedEnabled {
		attempts = []attempt{{guided: true}, {guided: false}}
	}
	for i, att := range attempts {
		var guided any
		if att.guided {
			guided = hintSchema
		}
		raw, err := a.chat(a.Cfg.Model, base, a.Cfg.Temperature, 300, guided)
		if err != nil {
			var apiErr *apiError
			unsupported := errors.As(err, &apiErr) &&
				(apiErr.Status == 400 || apiErr.Status == 422)
			if i == 0 && a.guidedEnabled && (unsupported ||
				strings.Contains(strings.ToLower(err.Error()), "guided")) {
				a.guidedEnabled = false
				a.logf("guided_json не поддержан, фолбэк на промпт: %v", err)
				continue
			}
			a.logf("ошибка запроса: %v", err)
			a.backoff = min(a.backoff*2+5, 60)
			return nil
		}
		parsed := parseJSON(raw)
		if parsed == nil {
			a.logf("не удалось разобрать ответ: %.200s", raw)
			a.backoff = min(a.backoff+5, 60)
			return nil
		}
		return a.toHint(parsed)
	}
	return nil
}

func (a *Analyzer) toHint(parsed map[string]any) *Hint {
	hint := clampStr(strAny(parsed["hint"]), a.Cfg.MaxHintChars)
	note := strAny(parsed["note"])
	importance := strAny(parsed["importance"])
	if importance != "low" && importance != "medium" && importance != "high" {
		importance = "low"
	}
	if hint == "" && note == "" {
		a.lastAnalysis = now()
		a.mu.Lock()
		a.newWords = 0
		a.mu.Unlock()
		return nil
	}
	return &Hint{Hint: hint, Note: note, Importance: importance,
		Ts: now(), Model: a.Cfg.Model}
}

func strAny(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func clampStr(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		r = r[:max]
	}
	return strings.TrimSpace(string(r))
}

var fenceRe = regexp.MustCompile("(?s)^```(?:json)?\\s*|\\s*```$")

func parseJSON(raw string) map[string]any {
	text := strings.TrimSpace(raw)
	text = fenceRe.ReplaceAllString(text, "")
	if obj := unmarshalObject(text); obj != nil {
		return obj
	}
	start := strings.Index(text, "{")
	if start < 0 {
		return nil
	}
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return unmarshalObject(text[start : i+1])
			}
		}
	}
	return nil
}

func unmarshalObject(text string) map[string]any {
	var obj map[string]any
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return nil
	}
	return obj
}

func (a *Analyzer) Stop() {
	a.once.Do(func() { close(a.stop) })
	select {
	case <-a.done:
	case <-time.After(8 * time.Second):
	}
}
