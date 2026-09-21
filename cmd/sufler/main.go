package main

import (
	"flag"
	"fmt"
	"os"

	"sufler-go/internal/config"
	"sufler-go/internal/db"
	"sufler-go/internal/logutil"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "", "путь к config.yaml")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	check := flag.Bool("check", false, "проверить конфиг и БД и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println("sufler", version)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(err)
	}

	if *check {
		if err := runCheck(cfg); err != nil {
			fatal(err)
		}
		return
	}

	fmt.Println("sufler:", version)
	fmt.Println("пайплайн захвата/ASR/LLM ещё не перенесён (в разработке)")
}

func runCheck(cfg *config.Config) error {
	d, err := db.Open(cfg.DB.Path)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer d.Close()

	id, err := d.StartSession("check")
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	if err := d.EndSession(id); err != nil {
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

	fmt.Println("config:     ok")
	fmt.Printf("  db:       %s (сессия %d)\n", cfg.DB.Path, id)
	fmt.Printf("  log:      %s\n", cfg.LogPath)
	fmt.Printf("  prompt:   %s (%d байт)\n", cfg.PromptPath, len(prompt))
	fmt.Printf("  asr:      %s (%s/%s)\n",
		cfg.ASR.Model, cfg.ASR.Device, cfg.ASR.ComputeType)
	fmt.Printf("  llm:      %s @ %s\n", cfg.LLM.Model, cfg.LLM.BaseURL)
	fmt.Printf("  audio:    %d Hz, block %d ms, backend %s\n",
		cfg.Audio.Samplerate, cfg.Audio.BlockMs, cfg.Audio.Backend)
	fmt.Println("check:      ok")
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "sufler: ошибка:", err)
	os.Exit(1)
}
