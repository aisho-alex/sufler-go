package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"sufler-go/internal/analyzer"
	"sufler-go/internal/asr"
	"sufler-go/internal/bus"
	"sufler-go/internal/capture"
	"sufler-go/internal/config"
	"sufler-go/internal/db"
	"sufler-go/internal/logutil"
	"sufler-go/internal/vad"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "", "путь к config.yaml")
	noUI := flag.Bool("no-ui", false, "консольный режим вместо оверлея")
	noMic := flag.Bool("no-mic", false, "без микрофона")
	noMonitor := flag.Bool("no-monitor", false, "без монитора системы")
	model := flag.String("model", "", "модель whisper (переопределяет config)")
	lang := flag.String("lang", "", "язык ASR (переопределяет config)")
	note := flag.String("note", "", "заметка к сессии")
	listDevices := flag.Bool("list-devices", false, "показать аудио-устройства и выйти")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	check := flag.Bool("check", false, "проверить конфиг и БД и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println("sufler", version)
		return
	}
	if *listDevices {
		fmt.Println(capture.ListDevices())
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(err)
	}
	if *model != "" {
		cfg.ASR.Model = *model
	}
	if *lang != "" {
		cfg.ASR.Language = *lang
	}

	if *check {
		if err := runCheck(cfg); err != nil {
			fatal(err)
		}
		return
	}

	app, err := NewApp(cfg, *noMic, *noMonitor, *note)
	if err != nil {
		fatal(err)
	}
	if *noUI {
		runConsole(app)
		return
	}
	fmt.Println("суфлёр: оверлей появится в фазе 6, запускаю консольный режим")
	runConsole(app)
}

type unit struct {
	stream *capture.AudioStream
	seg    *vad.Segmenter
}

type App struct {
	cfg       *config.Config
	flog      *logutil.FileLog
	bus       *bus.Bus
	store     *db.DB
	sessionID int64

	asr      *asr.ASR
	ana      *analyzer.Analyzer
	vadModel *vad.Model
	units    []unit

	noMic, noMonitor bool
	closed           bool
	mu               sync.Mutex
}

func NewApp(cfg *config.Config, noMic, noMonitor bool, note string) (*App, error) {
	flog, err := logutil.NewFileLog(cfg.LogPath, true)
	if err != nil {
		return nil, err
	}
	store, err := db.Open(cfg.DB.Path)
	if err != nil {
		flog.Close()
		return nil, err
	}
	sessionID, err := store.StartSession(note)
	if err != nil {
		store.Close()
		flog.Close()
		return nil, err
	}
	suffix := ""
	if note != "" {
		suffix = " (" + note + ")"
	}
	flog.Logf("=== сессия %d start%s ===", sessionID, suffix)
	return &App{
		cfg:       cfg,
		flog:      flog,
		bus:       &bus.Bus{},
		store:     store,
		sessionID: sessionID,
		noMic:     noMic,
		noMonitor: noMonitor,
	}, nil
}

func (a *App) log(msg string) { a.flog.Log(msg) }

func (a *App) startWorkers() error {
	model, err := vad.NewModel(vad.ModelsDir())
	if err != nil {
		return fmt.Errorf("vad: %w", err)
	}
	a.vadModel = model

	a.asr = asr.New(a.cfg.ASR.Model, a.cfg.ASR.Language, a.cfg.ASR.BeamSize,
		a.onResult, a.log)
	if err := a.asr.Start(vad.ModelsDir()); err != nil {
		return err
	}

	prompt, err := a.cfg.Prompt()
	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	a.ana = analyzer.New(&a.cfg.LLM, prompt)
	a.ana.OnHint = a.onHint
	a.ana.OnAnswer = a.onAnswer
	a.ana.OnStatus = func(s string) { a.bus.Publish("status", map[string]any{"text": s}) }
	a.ana.Log = a.log
	a.ana.Start()
	return nil
}

func (a *App) startStreams() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	labels := []string{}
	if !a.noMic {
		labels = append(labels, "mic")
	}
	if !a.noMonitor {
		labels = append(labels, "monitor")
	}
	for _, label := range labels {
		label := label
		seg := vad.NewSegmenter(a.vadModel,
			a.cfg.VAD.Threshold,
			a.cfg.VAD.MinSpeechMs,
			a.cfg.VAD.MinSilenceMs,
			a.cfg.VAD.MaxSegmentMs,
			a.cfg.VAD.PadMs,
			a.cfg.Audio.Samplerate,
			func(s []float32, tsStart, tsEnd float64) {
				a.submit(s, tsStart, tsEnd, label)
			})
		stream := capture.NewAudioStream(label, a.cfg.Audio.Backend,
			a.cfg.Audio.Samplerate, a.cfg.Audio.BlockMs,
			func(_ string, block []float32) { seg.Feed(block) }, a.log)
		stream.Start()
		a.units = append(a.units, unit{stream: stream, seg: seg})
	}
	if len(a.units) > 0 {
		names := make([]string, 0, len(a.units))
		for _, u := range a.units {
			names = append(names, u.stream.Label)
		}
		a.log("источники: " + strings.Join(names, ", "))
	}
}

func (a *App) stopStreams() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, u := range a.units {
		u.seg.Flush()
	}
	for _, u := range a.units {
		u.stream.Stop()
	}
	a.units = nil
}

func (a *App) submit(seg []float32, tsStart, tsEnd float64, source string) {
	if a.asr != nil {
		a.asr.Submit(seg, tsStart, tsEnd, source)
	}
}

func (a *App) onResult(ev asr.Result) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.store.SaveSegment(a.sessionID, ev.TsStart, ev.TsEnd, ev.Source, ev.Text)
	a.flog.LogSilent("[transcript/" + ev.Source + "] " + ev.Text)
	a.bus.Publish("transcript", map[string]any{
		"source": ev.Source, "ts_start": ev.TsStart, "ts_end": ev.TsEnd,
		"text": ev.Text, "latency": ev.Latency,
	})
	if a.ana != nil {
		a.ana.AddTranscript(ev.Source, ev.Text)
	}
}

func (a *App) onHint(h analyzer.Hint) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.store.SaveHint(a.sessionID, h.Hint, h.Importance, h.Model)
	a.store.SaveNote(a.sessionID, h.Note, "", "llm")
	parts := []string{}
	if h.Hint != "" {
		parts = append(parts, h.Hint)
	}
	if h.Note != "" {
		parts = append(parts, "note: "+h.Note)
	}
	joined := "-"
	if len(parts) > 0 {
		joined = strings.Join(parts, " | ")
	}
	a.log("[hint/" + h.Importance + "] " + joined)
	a.bus.Publish("hint", map[string]any{
		"hint": h.Hint, "note": h.Note, "importance": h.Importance,
		"ts": h.Ts, "model": h.Model,
	})
}

func (a *App) onAnswer(question, answer string) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.store.SaveQA(a.sessionID, question, answer)
	a.log("[answer] " + orDash(answer))
	a.bus.Publish("answer", map[string]any{
		"question": question, "answer": answer, "ts": nowF(),
	})
}

func (a *App) userNote(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return
	}
	a.store.SaveNote(a.sessionID, text, "", "user")
	a.log("[note/user] " + text)
	a.bus.Publish("hint", map[string]any{
		"note": text, "source": "user", "ts": nowF(),
	})
	if a.ana != nil {
		a.ana.AddUserNote(text)
	}
}

func (a *App) userQuestion(question string) {
	question = strings.TrimSpace(question)
	if question == "" {
		return
	}
	a.log("[question] " + question)
	if a.ana != nil {
		a.ana.Ask(question)
	} else {
		a.log("[answer] анализатор ещё не запущен")
	}
}

func (a *App) shutdown() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.mu.Unlock()

	a.stopStreams()
	if a.asr != nil {
		a.asr.Stop()
	}
	if a.ana != nil {
		a.ana.Stop()
	}
	if a.vadModel != nil {
		a.vadModel.Destroy()
	}
	a.store.EndSession(a.sessionID)
	a.store.Close()
	a.flog.Log("=== сессия end ===")
	a.log("сессия сохранена")
	a.flog.Close()
}

func runConsole(app *App) {
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	app.bus.Subscribe(func(e bus.Event) {
		switch e.Type {
		case "transcript":
			who := "собеседник"
			if e.Data["source"] == "mic" {
				who = "я"
			}
			fmt.Printf("[%s] %s\n", who, e.Data["text"])
		case "status":
			if text, _ := e.Data["text"].(string); text != "" {
				low := strings.ToLower(text)
				if strings.Contains(low, "ошиб") || strings.Contains(low, "упал") {
					fmt.Println("· " + text)
				}
			}
		}
	})

	if err := app.startWorkers(); err != nil {
		app.shutdown()
		fatal(err)
	}
	app.startStreams()
	fmt.Println("Запущено. Ctrl+C — выход.")
	fmt.Println("Ввод: текст — заметка, '?вопрос' — спросить LLM.")

	go stdinLoop(app)

	<-ctx.Done()
	app.shutdown()
}

func stdinLoop(app *App) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "?") {
			app.userQuestion(line[1:])
		} else {
			app.userNote(line)
		}
	}
}

func nowF() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func orDash(s string) string {
	if s == "" {
		return "(нет ответа)"
	}
	return s
}

func runCheck(cfg *config.Config) error {
	store, err := db.Open(cfg.DB.Path)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer store.Close()

	id, err := store.StartSession("check")
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	if err := store.EndSession(id); err != nil {
		return fmt.Errorf("db: %w", err)
	}

	flog, err := logutil.NewFileLog(cfg.LogPath, false)
	if err != nil {
		return fmt.Errorf("log: %w", err)
	}
	flog.Logf("=== check сессия %d ok ===", id)
	flog.Close()

	prompt, err := cfg.Prompt()
	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}

	modelPath := vad.ModelsDir() + "/ggml-" + cfg.ASR.Model + ".bin"
	if _, err := os.Stat(modelPath); err != nil {
		fmt.Printf("  asr:      модель не найдена: %s\n", modelPath)
	} else {
		fmt.Printf("  asr:      %s (%s/%s) ok\n",
			cfg.ASR.Model, cfg.ASR.Device, cfg.ASR.ComputeType)
	}

	fmt.Println("config:     ok")
	fmt.Printf("  db:       %s (сессия %d)\n", cfg.DB.Path, id)
	fmt.Printf("  log:      %s\n", cfg.LogPath)
	fmt.Printf("  prompt:   %s (%d байт)\n", cfg.PromptPath, len(prompt))
	fmt.Printf("  llm:      %s @ %s (ключ: %s)\n",
		cfg.LLM.Model, cfg.LLM.BaseURL, keyState(cfg.LLM.APIKey))
	fmt.Printf("  audio:    %d Hz, block %d ms, backend %s\n",
		cfg.Audio.Samplerate, cfg.Audio.BlockMs, cfg.Audio.Backend)
	fmt.Println("check:      ok")
	return nil
}

func keyState(key string) string {
	if key == "" {
		return "НЕ ЗАДАН"
	}
	return "задан"
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "sufler: ошибка:", err)
	os.Exit(1)
}
