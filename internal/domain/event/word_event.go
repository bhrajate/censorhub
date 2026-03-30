package event

import "time"

// EventType 事件类型
type EventType string

const (
	WordCreated       EventType = "word.created"
	WordUpdated       EventType = "word.updated"
	WordDeleted       EventType = "word.deleted"
	WordBatchImported EventType = "word.batch_imported"
)

// WordChangedEvent 词条变更事件
type WordChangedEvent struct {
	Type      EventType `json:"type"`
	WordID    uint64    `json:"word_id,omitempty"`
	Count     int       `json:"count,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

func NewWordChangedEvent(eventType EventType, wordID uint64) *WordChangedEvent {
	return &WordChangedEvent{
		Type:      eventType,
		WordID:    wordID,
		Timestamp: time.Now(),
	}
}

func NewBatchImportEvent(count int) *WordChangedEvent {
	return &WordChangedEvent{
		Type:      WordBatchImported,
		Count:     count,
		Timestamp: time.Now(),
	}
}
