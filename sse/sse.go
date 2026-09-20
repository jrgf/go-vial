package sse

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"
)

const (
	// DefaultBuffer is the number of pending events allowed per subscriber.
	DefaultBuffer = 16
	// DefaultHeartbeat is the interval between heartbeat comments.
	DefaultHeartbeat = 15 * time.Second
	// DefaultWriteTimeout bounds each event or heartbeat write.
	DefaultWriteTimeout = 10 * time.Second
)

// SlowConsumerPolicy controls what happens when a subscriber buffer is full.
type SlowConsumerPolicy uint8

const (
	// DisconnectSlowConsumer closes a subscriber whose buffer is full.
	DisconnectSlowConsumer SlowConsumerPolicy = iota
	// DropEvent keeps the subscriber and drops the new event for that subscriber.
	DropEvent
)

// HubConfig configures a Hub.
type HubConfig struct {
	// SubscriberBuffer bounds pending events for each subscriber. Zero uses 16.
	SubscriberBuffer int
	// SlowConsumer chooses disconnect or per-subscriber event dropping.
	SlowConsumer SlowConsumerPolicy
	// Heartbeat controls comment frequency. Zero uses 15 seconds.
	Heartbeat time.Duration
	// WriteTimeout bounds each network write. Zero uses 10 seconds.
	WriteTimeout time.Duration
}

// Hub fans events out to bounded subscriber channels.
type Hub struct {
	mu           sync.Mutex
	subscribers  map[string]map[*subscriber]struct{}
	buffer       int
	policy       SlowConsumerPolicy
	heartbeat    time.Duration
	writeTimeout time.Duration
}

type subscriber struct {
	topic  string
	events chan Event
	stop   func() bool
}

// NewHub creates a bounded event hub.
func NewHub(config HubConfig) (*Hub, error) {
	if config.SubscriberBuffer < 0 {
		return nil, errors.New("sse: subscriber buffer cannot be negative")
	}
	if config.SubscriberBuffer == 0 {
		config.SubscriberBuffer = DefaultBuffer
	}
	if config.Heartbeat < 0 {
		return nil, errors.New("sse: heartbeat cannot be negative")
	}
	if config.Heartbeat == 0 {
		config.Heartbeat = DefaultHeartbeat
	}
	if config.WriteTimeout < 0 {
		return nil, errors.New("sse: write timeout cannot be negative")
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = DefaultWriteTimeout
	}
	if config.SlowConsumer != DisconnectSlowConsumer && config.SlowConsumer != DropEvent {
		return nil, errors.New("sse: invalid slow-consumer policy")
	}
	return &Hub{
		subscribers:  make(map[string]map[*subscriber]struct{}),
		buffer:       config.SubscriberBuffer,
		policy:       config.SlowConsumer,
		heartbeat:    config.Heartbeat,
		writeTimeout: config.WriteTimeout,
	}, nil
}

// Subscribe returns a bounded event channel that closes when the context is
// canceled, the subscriber falls behind under DisconnectSlowConsumer, or the
// hub closes.
func (hub *Hub) Subscribe(contextValue context.Context) <-chan Event {
	return hub.SubscribeTopic(contextValue, "")
}

// SubscribeTopic returns events published to one opaque topic. An empty topic
// is the default broadcast channel used by Subscribe and Publish.
func (hub *Hub) SubscribeTopic(contextValue context.Context, topic string) <-chan Event {
	if contextValue == nil {
		contextValue = context.Background()
	}
	subscription := &subscriber{
		topic:  topic,
		events: make(chan Event, hub.buffer),
	}
	if contextValue.Err() != nil {
		close(subscription.events)
		return subscription.events
	}

	hub.mu.Lock()
	if hub.subscribers == nil {
		hub.mu.Unlock()
		close(subscription.events)
		return subscription.events
	}
	if hub.subscribers[topic] == nil {
		hub.subscribers[topic] = make(map[*subscriber]struct{})
	}
	hub.subscribers[topic][subscription] = struct{}{}
	subscription.stop = context.AfterFunc(contextValue, func() { hub.remove(subscription) })
	hub.mu.Unlock()
	return subscription.events
}

// Publish snapshots an event and offers it to every current subscriber. It
// returns the number of subscribers that accepted the event.
func (hub *Hub) Publish(event Event) (int, error) {
	return hub.PublishTopic("", event)
}

// PublishTopic offers an event only to subscribers of topic.
func (hub *Hub) PublishTopic(topic string, event Event) (int, error) {
	if err := validateEvent(event); err != nil {
		return 0, err
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.subscribers == nil {
		return 0, nil
	}
	delivered := 0
	for subscription := range hub.subscribers[topic] {
		snapshot := event
		snapshot.Data = bytes.Clone(event.Data)
		select {
		case subscription.events <- snapshot:
			delivered++
		default:
			if hub.policy == DisconnectSlowConsumer {
				hub.disconnectLocked(subscription)
			}
		}
	}
	return delivered, nil
}

// Close disconnects every subscriber. Future subscriptions are closed and
// future publications are ignored.
func (hub *Hub) Close() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.subscribers == nil {
		return
	}
	for _, subscriptions := range hub.subscribers {
		for subscription := range subscriptions {
			subscription.stop()
			close(subscription.events)
		}
	}
	hub.subscribers = nil
}

func (hub *Hub) remove(subscription *subscriber) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.disconnectLocked(subscription)
}

func (hub *Hub) disconnectLocked(subscription *subscriber) {
	subscriptions := hub.subscribers[subscription.topic]
	if _, exists := subscriptions[subscription]; !exists {
		return
	}
	delete(subscriptions, subscription)
	if len(subscriptions) == 0 {
		delete(hub.subscribers, subscription.topic)
	}
	subscription.stop()
	close(subscription.events)
}
