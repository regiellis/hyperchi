package hyperchi

import (
	"log"
	"sync"
)

// EventBus (app.Events) delivers named events to subscribers. It is safe for
// concurrent use. The framework emits content:*, scheduler:task:*, and other
// events on it.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[string][]func(any)
}

// newEventBus returns a bus with no subscribers.
func newEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[string][]func(any)),
	}
}

// Emit calls each subscriber of event with data, each in its own goroutine,
// and returns without waiting. Delivery order is not guaranteed, and a
// panicking handler is logged. The subscriber list is copied under the read
// lock so concurrent On and Off calls cannot race with dispatch.
func (eb *EventBus) Emit(event string, data any) {
	eb.mu.RLock()
	subscribers := eb.subscribers[event]
	handlers := make([]func(any), len(subscribers))
	copy(handlers, subscribers)
	eb.mu.RUnlock()

	for _, handler := range handlers {
		go func(h func(any)) {
			// A panicking subscriber must not take down the process; the
			// scheduler emits events, so this path is reachable in-framework.
			defer func() {
				if r := recover(); r != nil {
					log.Printf("hyperchi: event handler for %q panicked: %v", event, r)
				}
			}()
			h(data)
		}(handler)
	}
}

// On adds handler as a subscriber of event.
func (eb *EventBus) On(event string, handler func(any)) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.subscribers[event] = append(eb.subscribers[event], handler)
}

// Off removes every subscriber of event.
func (eb *EventBus) Off(event string) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	delete(eb.subscribers, event)
}

// SubscriberCount returns the number of handlers subscribed to event.
func (eb *EventBus) SubscriberCount(event string) int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.subscribers[event])
}

// EventStats returns a snapshot of each event name's handler count, which
// (unlike the handler map) serializes to JSON.
func (eb *EventBus) EventStats() map[string]int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	stats := make(map[string]int, len(eb.subscribers))
	for event, handlers := range eb.subscribers {
		stats[event] = len(handlers)
	}
	return stats
}
