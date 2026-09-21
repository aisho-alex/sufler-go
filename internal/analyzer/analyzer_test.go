package analyzer

import (
	"testing"

	"sufler-go/internal/config"
)

func TestParseJSON(t *testing.T) {
	cases := []struct {
		name, in string
		wantHint string
	}{
		{"plain", `{"hint": "перезвонить", "importance": "high", "note": null}`,
			"перезвонить"},
		{"fenced", "```json\n{\"hint\": \"ok\", \"importance\": \"low\", \"note\": null}\n```",
			"ok"},
		{"prose", `Ответ: {"hint": "да", "importance": "medium", "note": "n"} конец`,
			"да"},
		{"nested", `{"hint": "a{b}c", "importance": "low", "note": null}`, "a{b}c"},
		{"newline-fence", "```json\n{\"hint\": \"многострочный\",\n \"importance\": \"low\",\n \"note\": null}\n```",
			"многострочный"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			obj := parseJSON(c.in)
			if obj == nil {
				t.Fatalf("parseJSON(%q) = nil", c.in)
			}
			if got := strAny(obj["hint"]); got != c.wantHint {
				t.Fatalf("hint = %q, хочу %q", got, c.wantHint)
			}
		})
	}
	if parseJSON("совсем не json") != nil {
		t.Fatal("ожидался nil для мусора")
	}
}

func testAnalyzer(t *testing.T) *Analyzer {
	t.Helper()
	cfg := &config.LLM{
		MaxHintChars: 300, ContextSeconds: 180,
	}
	return New(cfg, "sys")
}

func TestToHintClampAndDefaults(t *testing.T) {
	a := testAnalyzer(t)
	long := make([]rune, 400)
	for i := range long {
		long[i] = 'а'
	}
	h := a.toHint(map[string]any{
		"hint": string(long), "note": "  заметка  ", "importance": "сверхважно",
	})
	if got := len([]rune(h.Hint)); got != 300 {
		t.Fatalf("hint не обрезан: %d", got)
	}
	if h.Note != "заметка" {
		t.Fatalf("note = %q", h.Note)
	}
	if h.Importance != "low" {
		t.Fatalf("importance = %q", h.Importance)
	}
}

func TestToHintEmptyResets(t *testing.T) {
	a := testAnalyzer(t)
	a.newWords = 100
	a.lastAnalysis = 0
	h := a.toHint(map[string]any{"hint": nil, "note": "", "importance": "low"})
	if h != nil {
		t.Fatal("ожидался nil")
	}
	if a.newWords != 0 {
		t.Fatal("newWords не сброшены")
	}
	if a.lastAnalysis == 0 {
		t.Fatal("lastAnalysis не обновлён")
	}
}

func TestWindowContext(t *testing.T) {
	cfg := &config.LLM{ContextSeconds: 180, MaxContextChars: 6000}
	a := New(cfg, "sys")
	a.remember("mic", "привет")
	a.remember("monitor", "здравствуйте")
	a.remember("user", "заметка тест")
	ctx := a.buildContext()
	want := "[я] привет\n[собеседник] здравствуйте\n[пользователь] заметка тест"
	if ctx != want {
		t.Fatalf("context = %q", ctx)
	}
	a.mu.Lock()
	nw := a.newWords
	a.mu.Unlock()
	if nw != 2 {
		t.Fatalf("newWords = %d, хочу 2 (только не-user)", nw)
	}
}
