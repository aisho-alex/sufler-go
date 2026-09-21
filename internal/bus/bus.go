package bus

import (
	"fmt"
	"sync"
)

type Event struct {
	Type string
	Data map[string]any
}

type Bus struct {
	mu   sync.Mutex
	subs []func(Event)
}

func (b *Bus) Subscribe(cb func(Event)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = append(b.subs, cb)
}

func (b *Bus) Publish(etype string, data map[string]any) {
	b.mu.Lock()
	subs := make([]func(Event), len(b.subs))
	copy(subs, b.subs)
	b.mu.Unlock()

	e := Event{Type: etype, Data: data}
	for _, cb := range subs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[bus] subscriber error (%s): %v\n", etype, r)
				}
			}()
			cb(e)
		}()
	}
}
