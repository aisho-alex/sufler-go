package ui

import (
	"fmt"
	"strings"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"

	"sufler-go/internal/config"
)

const css = `
#root {
	background-color: rgba(16, 18, 24, 0.97);
	border-radius: 14px;
	border: 1px solid rgba(255, 255, 255, 0.18);
}
label { color: #ffffff; font-size: 14px; }
button {
	color: #ffffff; background: rgba(255,255,255,0.13);
	border: none; border-radius: 9px; padding: 4px 10px;
	font-size: 14px; text-shadow: none;
}
button:hover { background: rgba(255,255,255,0.24); }
textview, text {
	background: transparent; color: #e6ebf2;
	font-size: 14px; caret-color: transparent;
}
text selection { background-color: #4a5568; }
entry {
	background: rgba(255,255,255,0.09); color: #ffffff;
	border: 1px solid rgba(255,255,255,0.16); border-radius: 9px;
	padding: 5px 9px; font-size: 13px;
}
entry:focus { border: 1px solid rgba(127,179,255,0.63); }
scrolledwindow { background: transparent; }
`

const (
	colorMic      = "#8fd18f"
	colorMonitor  = "#7fb3ff"
	colorHigh     = "#ff7a7a"
	colorMedium   = "#ffc46b"
	colorLow      = "#9aa7b8"
	colorAnswer   = "#8fd18f"
	colorQuestion = "#c9b6ff"
	colorNoteText = "#dbe4f0"
	colorText     = "#e6ebf2"
	colorMuted    = "#98a2b3"
	colorTitle    = "#aab4c4"
	maxHints      = 6
	pillW, pillH  = 400, 210
	marginCorner  = 28
)

type hintItem struct {
	kind       string
	question   string
	answer     string
	text       string
	hint       string
	note       string
	importance string
}

type Overlay struct {
	win    *gtk.Window
	dot    *gtk.Label
	title  *gtk.Label
	btnTog *gtk.Button
	btnPil *gtk.Button
	btnQut *gtk.Button

	hintScroll *gtk.ScrolledWindow
	hintLabel  *gtk.Label
	trScroll   *gtk.ScrolledWindow
	transcript *gtk.TextView
	trBuffer   *gtk.TextBuffer
	input      *gtk.Entry
	btnNote    *gtk.Button
	btnAsk     *gtk.Button

	cfg        *config.Config
	onToggle   func(bool)
	onQuit     func()
	onNote     func(string)
	onQuestion func(string)

	running, pill bool
	hints         []hintItem
	dragOffX      int
	dragOffY      int
	dragging      bool
}

func NewOverlay(cfg *config.Config, onToggle func(bool), onQuit func(),
	onNote, onQuestion func(string)) (*Overlay, error) {
	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		return nil, err
	}
	o := &Overlay{
		win:        win,
		cfg:        cfg,
		onToggle:   onToggle,
		onQuit:     onQuit,
		onNote:     onNote,
		onQuestion: onQuestion,
	}

	win.SetTitle("суфлёр")
	win.SetDecorated(false)
	win.SetKeepAbove(true)
	win.SetSkipTaskbarHint(true)
	win.SetSkipPagerHint(true)
	win.SetResizable(false)
	win.SetDefaultSize(cfg.UI.Width, cfg.UI.Height)
	win.SetOpacity(cfg.UI.Opacity)
	win.SetName("root")

	screen := win.GetScreen()
	if visual, err := screen.GetRGBAVisual(); err == nil && visual != nil {
		win.SetVisual(visual)
	}

	provider, err := gtk.CssProviderNew()
	if err != nil {
		return nil, err
	}
	if err := provider.LoadFromData(css); err != nil {
		return nil, err
	}
	gtk.AddProviderForScreen(screen, provider,
		uint(gtk.STYLE_PROVIDER_PRIORITY_APPLICATION))

	root, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 8)
	if err != nil {
		return nil, err
	}
	root.SetMarginTop(10)
	root.SetMarginBottom(12)
	root.SetMarginStart(14)
	root.SetMarginEnd(14)
	win.Add(root)

	top, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 6)
	o.dot, _ = gtk.LabelNew("●")
	o.dot.SetMarkup(`<span color="#666666">●</span>`)
	o.title, _ = gtk.LabelNew("суфлёр — офлайн")
	setLabelColor(o.title, colorTitle, 12)
	top.PackStart(o.dot, false, false, 0)
	top.PackStart(o.title, false, false, 0)

	spacer, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	top.PackEnd(spacer, true, true, 0)
	o.btnQut = newBtn("✕", 32, "Выход")
	o.btnPil = newBtn("▯", 32, "Свернуть в пилюлю")
	o.btnTog, _ = gtk.ButtonNewWithLabel("▶ старт")
	top.PackEnd(o.btnQut, false, false, 0)
	top.PackEnd(o.btnPil, false, false, 0)
	top.PackEnd(o.btnTog, false, false, 0)
	root.PackStart(top, false, false, 0)

	o.hintScroll, _ = gtk.ScrolledWindowNew(nil, nil)
	o.hintScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	o.hintScroll.SetSizeRequest(-1, 140)
	o.hintLabel, _ = gtk.LabelNew("")
	o.hintLabel.SetXAlign(0)
	o.hintLabel.SetLineWrap(true)
	o.hintScroll.Add(o.hintLabel)
	root.PackStart(o.hintScroll, false, false, 0)

	o.transcript, _ = gtk.TextViewNew()
	o.transcript.SetEditable(false)
	o.transcript.SetCursorVisible(false)
	o.transcript.SetWrapMode(gtk.WRAP_WORD)
	o.trBuffer, _ = o.transcript.GetBuffer()
	o.trScroll, _ = gtk.ScrolledWindowNew(nil, nil)
	o.trScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	o.trScroll.Add(o.transcript)
	root.PackStart(o.trScroll, true, true, 0)

	row, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 6)
	o.input, _ = gtk.EntryNew()
	o.input.SetPlaceholderText("Вопрос — Enter · Заметка — Ctrl+Enter")
	row.PackStart(o.input, true, true, 0)
	o.btnNote = newBtn("💾", 34, "Сохранить заметку (Ctrl+Enter)")
	o.btnAsk = newBtn("❓", 34, "Спросить LLM (Enter)")
	row.PackStart(o.btnNote, false, false, 0)
	row.PackStart(o.btnAsk, false, false, 0)
	root.PackStart(row, false, false, 0)

	o.btnTog.Connect("clicked", o.Toggle)
	o.btnPil.Connect("clicked", o.togglePill)
	o.btnQut.Connect("clicked", o.quit)
	o.btnNote.Connect("clicked", o.saveNote)
	o.btnAsk.Connect("clicked", o.ask)
	o.input.Connect("activate", o.ask)
	o.input.Connect("key-press-event", o.onInputKey)

	win.Connect("destroy", func() {
		if o.running {
			o.onToggle(false)
		}
		o.onQuit()
	})
	win.Connect("delete-event", func() bool {
		o.quit()
		return true
	})
	win.Connect("button-press-event", o.onPress)
	win.Connect("motion-notify-event", o.onMotion)
	win.AddEvents(int(gdk.BUTTON_PRESS_MASK) | int(gdk.POINTER_MOTION_MASK))

	o.renderHints()
	win.ShowAll()
	o.reposition()
	return o, nil
}

func newBtn(label string, width int, tooltip string) *gtk.Button {
	b, _ := gtk.ButtonNewWithLabel(label)
	b.SetSizeRequest(width, -1)
	b.SetTooltipText(tooltip)
	return b
}

func setLabelColor(l *gtk.Label, color string, size int) {
	text, _ := l.GetText()
	l.SetMarkup(fmt.Sprintf(`<span color="%s" size="%d00">%s</span>`,
		color, size, escaped(text)))
}

func escaped(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func (o *Overlay) onPress(_ *gtk.Window, ev *gdk.Event) bool {
	bev := gdk.EventButtonNewFromEvent(ev)
	if bev.Button() != 1 {
		return false
	}
	x, y := o.win.GetPosition()
	o.dragOffX = int(bev.XRoot()) - x
	o.dragOffY = int(bev.YRoot()) - y
	o.dragging = true
	return true
}

func (o *Overlay) onMotion(_ *gtk.Window, ev *gdk.Event) bool {
	if !o.dragging {
		return false
	}
	mev := gdk.EventMotionNewFromEvent(ev)
	x, y := mev.MotionValRoot()
	o.win.Move(int(x)-o.dragOffX, int(y)-o.dragOffY)
	return true
}

func (o *Overlay) reposition() {
	display, err := gdk.DisplayGetDefault()
	if err != nil {
		return
	}
	w, h := o.win.GetSize()
	var wa *gdk.Rectangle
	if gdkWin, err := o.win.GetWindow(); err == nil && gdkWin != nil {
		mon, err := display.GetMonitorAtWindow(gdkWin)
		if err == nil {
			wa = mon.GetWorkarea()
		}
	}
	if wa == nil {
		mon, err := display.GetPrimaryMonitor()
		if err != nil {
			return
		}
		wa = mon.GetWorkarea()
	}
	wx, wy, ww, wh := wa.GetRectangleInt()
	o.win.Move(wx+ww-w-marginCorner, wy+wh-h-marginCorner)
}

func (o *Overlay) Toggle() {
	o.running = !o.running
	if o.running {
		o.btnTog.SetLabel("■ стоп")
	} else {
		o.btnTog.SetLabel("▶ старт")
	}
	o.onToggle(o.running)
}

func (o *Overlay) togglePill() {
	o.pill = !o.pill
	o.trScroll.SetVisible(!o.pill)
	o.btnTog.SetVisible(!o.pill)
	o.dot.SetVisible(!o.pill)
	o.title.SetVisible(!o.pill)
	if o.pill {
		o.win.Resize(pillW, pillH)
	} else {
		o.win.Resize(o.cfg.UI.Width, o.cfg.UI.Height)
	}
	o.reposition()
}

func (o *Overlay) quit() {
	if o.running {
		o.onToggle(false)
		o.running = false
	}
	o.onQuit()
}

func (o *Overlay) ask() {
	text, _ := o.input.GetText()
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	o.input.SetText("")
	o.onQuestion(text)
}

func (o *Overlay) saveNote() {
	text, _ := o.input.GetText()
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	o.input.SetText("")
	o.onNote(text)
}

func (o *Overlay) onInputKey(_ *gtk.Entry, ev *gdk.Event) bool {
	kev := gdk.EventKeyNewFromEvent(ev)
	if (kev.KeyVal() == gdk.KEY_Return || kev.KeyVal() == gdk.KEY_KP_Enter) &&
		kev.State()&uint(gdk.CONTROL_MASK) != 0 {
		o.saveNote()
		return true
	}
	return false
}

func (o *Overlay) AddTranscript(source, text string) {
	color := colorText
	who := "собеседник"
	if source == "mic" {
		who = "я"
		color = colorMic
	} else if source == "monitor" {
		color = colorMonitor
	}
	markup := fmt.Sprintf(`<span color="%s">[%s]</span> <span color="%s">%s</span>`+"\n",
		color, escaped(who), colorText, escaped(text))
	end := o.trBuffer.GetEndIter()
	o.trBuffer.InsertMarkup(end, markup)
	mark := o.trBuffer.CreateMark("end", o.trBuffer.GetEndIter(), false)
	o.transcript.ScrollToMark(mark, 0, false, 0, 1)
	o.trBuffer.DeleteMark(mark)
}

func (o *Overlay) AddHint(data map[string]any) {
	var item hintItem
	if q, ok := data["question"]; ok {
		item.kind = "qa"
		item.question = strAny(q)
		item.answer = strAny(data["answer"])
	} else if strAny(data["source"]) == "user" {
		item.kind = "user_note"
		item.text = strAny(data["note"])
	} else {
		item.kind = "llm"
		item.hint = strAny(data["hint"])
		item.note = strAny(data["note"])
		item.importance = strAny(data["importance"])
	}
	o.hints = append([]hintItem{item}, o.hints...)
	if len(o.hints) > maxHints {
		o.hints = o.hints[:maxHints]
	}
	o.renderHints()
}

func (o *Overlay) AddAnswer(question, answer string) {
	o.AddHint(map[string]any{"question": question, "answer": answer})
}

func (o *Overlay) SetStatus(s string) {
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "анализирую"), strings.Contains(low, "отвечаю"):
		o.dot.SetMarkup(fmt.Sprintf(`<span color="%s">●</span>`, colorMedium))
		o.title.SetText("суфлёр — " + s)
		o.title.SetTooltipText(s)
	case strings.Contains(low, "запущен"), strings.Contains(low, "источники"):
		o.dot.SetMarkup(`<span color="#69db7c">●</span>`)
		o.title.SetText("суфлёр — запись")
		o.title.SetTooltipText(s)
	case strings.Contains(low, "ошиб"), strings.Contains(low, "не найдено"),
		strings.Contains(low, "упал"):
		o.dot.SetMarkup(fmt.Sprintf(`<span color="%s">●</span>`, colorHigh))
		o.title.SetText("суфлёр — проблема")
		o.title.SetTooltipText(s)
	default:
		o.title.SetTooltipText(s)
	}
}

func (o *Overlay) renderHints() {
	if len(o.hints) == 0 {
		o.hintLabel.SetMarkup(
			fmt.Sprintf(`<span color="%s">Подсказки появятся здесь</span>`, colorMuted))
		return
	}
	var blocks []string
	for _, h := range o.hints {
		switch h.kind {
		case "user_note":
			blocks = append(blocks, fmt.Sprintf(
				`<span color="%s" weight="bold">👤 заметка</span> <span color="%s">%s</span>`,
				colorMonitor, colorNoteText, escaped(h.text)))
		case "qa":
			blocks = append(blocks, fmt.Sprintf(
				`<span color="%s" weight="bold">❓ вопрос</span> <span color="#c5cbe0">%s</span>`,
				colorQuestion, escaped(h.question)))
			if h.answer != "" {
				blocks = append(blocks, fmt.Sprintf(
					`<span color="%s">💬 %s</span>`, colorAnswer, escaped(h.answer)))
			} else {
				blocks = append(blocks, fmt.Sprintf(
					`<span color="%s">💬 …нет ответа</span>`, colorMuted))
			}
		default:
			color, label := colorLow, ""
			switch h.importance {
			case "high":
				color, label = colorHigh, " ВАЖНО"
			case "medium":
				color, label = colorMedium, " полезно"
			}
			if h.hint != "" {
				blocks = append(blocks, fmt.Sprintf(
					`<span color="%s" weight="bold">●%s</span> <span color="#f2f4f8">%s</span>`,
					color, label, escaped(h.hint)))
			}
			if h.note != "" {
				blocks = append(blocks, fmt.Sprintf(
					`<span color="%s">  💾 %s</span>`, color, escaped(h.note)))
			}
		}
	}
	o.hintLabel.SetMarkup(strings.Join(blocks, "\n"))
}

func strAny(v any) string {
	s, _ := v.(string)
	return s
}
