package config

import (
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type Audio struct {
	Samplerate    int    `yaml:"samplerate"`
	BlockMs       int    `yaml:"block_ms"`
	Backend       string `yaml:"backend"`
	MicDevice     *int   `yaml:"mic_device"`
	MonitorDevice *int   `yaml:"monitor_device"`
}

type VAD struct {
	Threshold    float64 `yaml:"threshold"`
	MinSpeechMs  int     `yaml:"min_speech_ms"`
	MinSilenceMs int     `yaml:"min_silence_ms"`
	MaxSegmentMs int     `yaml:"max_segment_ms"`
	PadMs        int     `yaml:"pad_ms"`
}

type ASR struct {
	Model       string `yaml:"model"`
	Device      string `yaml:"device"`
	ComputeType string `yaml:"compute_type"`
	Language    string `yaml:"language"`
	BeamSize    int    `yaml:"beam_size"`
}

type LLM struct {
	BaseURL         string  `yaml:"base_url"`
	Model           string  `yaml:"model"`
	APIKey          string  `yaml:"-"`
	Temperature     float64 `yaml:"temperature"`
	TimeoutS        float64 `yaml:"timeout_s"`
	MinIntervalS    float64 `yaml:"min_interval_s"`
	TriggerWords    int     `yaml:"trigger_words"`
	TriggerSeconds  float64 `yaml:"trigger_seconds"`
	ContextSeconds  int     `yaml:"context_seconds"`
	MaxContextChars int     `yaml:"max_context_chars"`
	MaxHintChars    int     `yaml:"max_hint_chars"`
	GuidedJSON      bool    `yaml:"guided_json"`
}

type DB struct {
	Path string `yaml:"path"`
}

type UI struct {
	Width   int     `yaml:"width"`
	Height  int     `yaml:"height"`
	Opacity float64 `yaml:"opacity"`
}

type Config struct {
	ProjectRoot string `yaml:"-"`

	Audio Audio `yaml:"audio"`
	VAD   VAD   `yaml:"vad"`
	ASR   ASR   `yaml:"asr"`
	LLM   LLM   `yaml:"llm"`
	DB    DB    `yaml:"db"`
	UI    UI    `yaml:"ui"`

	PromptPath string `yaml:"prompt_path"`
	LogPath    string `yaml:"log_path"`
}

func defaults() Config {
	return Config{
		Audio: Audio{Samplerate: 16000, BlockMs: 250, Backend: "sounddevice"},
		VAD: VAD{
			Threshold:    0.5,
			MinSpeechMs:  250,
			MinSilenceMs: 600,
			MaxSegmentMs: 15000,
			PadMs:        120,
		},
		ASR: ASR{Model: "small", Device: "cuda", ComputeType: "float16",
			Language: "ru", BeamSize: 1},
		LLM: LLM{
			Temperature:     0.3,
			TimeoutS:        25,
			MinIntervalS:    8,
			TriggerWords:    40,
			TriggerSeconds:  20,
			ContextSeconds:  180,
			MaxContextChars: 6000,
			MaxHintChars:    300,
			GuidedJSON:      true,
		},
		DB:         DB{Path: "data/sufler.db"},
		UI:         UI{Width: 470, Height: 340, Opacity: 0.88},
		PromptPath: "prompts/default.txt",
		LogPath:    "data/sufler.log",
	}
}

func Load(path string) (*Config, error) {
	cwd, err := filepath.Abs(".")
	if err != nil {
		return nil, err
	}
	root := cwd
	cfgPath := path
	if cfgPath != "" {
		if !filepath.IsAbs(cfgPath) {
			cfgPath = filepath.Join(cwd, cfgPath)
		}
	} else {
		root, cfgPath = findConfig(cwd)
	}
	_ = godotenv.Load(filepath.Join(root, ".env"))

	cfg := defaults()
	cfg.ProjectRoot = root

	if data, err := os.ReadFile(cfgPath); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
	}

	if cfg.LLM.BaseURL == "" {
		cfg.LLM.BaseURL = envOr("NEURALDEEP_BASE_URL", "https://api.neuraldeep.ru/v1")
	}
	if cfg.LLM.Model == "" {
		cfg.LLM.Model = envOr("SUFLER_MODEL", "qwen3.8-27b-noreason")
	}
	cfg.LLM.APIKey = os.Getenv("NEURALDEEP_API_KEY")

	cfg.DB.Path = resolve(root, cfg.DB.Path)
	cfg.LogPath = resolve(root, cfg.LogPath)
	return &cfg, nil
}

func findConfig(cwd string) (root, cfgPath string) {
	candidates := []string{cwd}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Dir(exeDir), exeDir)
	}
	for _, dir := range candidates {
		p := filepath.Join(dir, "config.yaml")
		if _, err := os.Stat(p); err == nil {
			return dir, p
		}
	}
	return cwd, filepath.Join(cwd, "config.yaml")
}

func (c *Config) Prompt() (string, error) {
	b, err := os.ReadFile(resolve(c.ProjectRoot, c.PromptPath))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func resolve(root, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
