package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const (
	persistAfter  = 30 * time.Second
	evictAfter    = 60 * time.Second
	cleanupPeriod = 5 * time.Second
)

type Message struct {
	Source  string `json:"source"`
	Content string `json:"content"`
}

type Conversation struct {
	UserKey        string
	ConversationID string
	OAID           string
	MiID           string
	InternalID     string

	mu          sync.Mutex
	InUse       int32
	History     []Message
	LastActive  time.Time
	LastPersist time.Time
	Dirty       bool
}

type Store struct {
	db *sql.DB

	mu    sync.RWMutex
	convs map[string]*Conversation

	userMu sync.RWMutex
	users  map[string]*User

	writeCh chan writeRequest
	stopCh  chan struct{}
}

type User struct {
	OAID string
	MiID string
}

type writeRequest struct {
	fn   func(*sql.Tx) error
	done chan error
}

func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL;`); err != nil {
		return nil, err
	}

	schema := `
CREATE TABLE IF NOT EXISTS users (
  user_key TEXT PRIMARY KEY,
  oaid TEXT NOT NULL,
  mi_id TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS conversations (
  user_key TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  internal_conv_id TEXT NOT NULL,
  history_json TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_key, conversation_id)
);
`
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}

	store := &Store{
		db:      db,
		convs:   make(map[string]*Conversation),
		users:   make(map[string]*User),
		writeCh: make(chan writeRequest, 1024),
		stopCh:  make(chan struct{}),
	}

	go store.writeLoop()
	go store.cleanupLoop()

	return store, nil
}

func (s *Store) Close() error {
	close(s.stopCh)
	close(s.writeCh)
	return s.db.Close()
}

func (s *Store) writeLoop() {
	for req := range s.writeCh {
		tx, err := s.db.Begin()
		if err != nil {
			if req.done != nil {
				req.done <- err
			}
			continue
		}
		if err := req.fn(tx); err != nil {
			_ = tx.Rollback()
			if req.done != nil {
				req.done <- err
			}
			continue
		}
		err = tx.Commit()
		if req.done != nil {
			req.done <- err
		}
	}
}

func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(cleanupPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
		}
		now := time.Now()
		var evictKeys []string

		s.mu.RLock()
		for key, conv := range s.convs {
			if atomic.LoadInt32(&conv.InUse) > 0 {
				continue
			}

			if conv.Dirty && now.Sub(conv.LastPersist) >= persistAfter {
				s.persistConversation(conv, now)
			}

			if now.Sub(conv.LastActive) >= evictAfter {
				evictKeys = append(evictKeys, key)
			}
		}
		s.mu.RUnlock()

		if len(evictKeys) == 0 {
			continue
		}

		s.mu.Lock()
		for _, key := range evictKeys {
			conv, ok := s.convs[key]
			if !ok {
				continue
			}
			if atomic.LoadInt32(&conv.InUse) > 0 {
				continue
			}
			s.persistConversation(conv, now)
			delete(s.convs, key)
		}
		s.mu.Unlock()
	}
}

func (s *Store) persistConversation(conv *Conversation, now time.Time) {
	conv.mu.Lock()
	historyCopy := append([]Message(nil), conv.History...)
	internalID := conv.InternalID
	userKey := conv.UserKey
	conversationID := conv.ConversationID
	conv.Dirty = false
	conv.LastPersist = now
	conv.mu.Unlock()

	historyJSON, err := json.Marshal(historyCopy)
	if err != nil {
		return
	}

	s.writeCh <- writeRequest{fn: func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO conversations (user_key, conversation_id, internal_conv_id, history_json, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(user_key, conversation_id)
			 DO UPDATE SET internal_conv_id=excluded.internal_conv_id, history_json=excluded.history_json, updated_at=excluded.updated_at`,
			userKey, conversationID, internalID, string(historyJSON), now.Unix(),
		)
		return err
	}}
}

func (s *Store) getOrCreateUser(userKey string) (string, string, error) {
	s.userMu.RLock()
	if user, ok := s.users[userKey]; ok {
		s.userMu.RUnlock()
		return user.OAID, user.MiID, nil
	}
	s.userMu.RUnlock()

	var oaid, miID string
	err := s.db.QueryRow(`SELECT oaid, mi_id FROM users WHERE user_key = ?`, userKey).Scan(&oaid, &miID)
	if err == nil {
		s.userMu.Lock()
		s.users[userKey] = &User{OAID: oaid, MiID: miID}
		s.userMu.Unlock()
		return oaid, miID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}

	oaid = newOAID()
	miID = newMiID()
	now := time.Now().Unix()

	done := make(chan error, 1)
	s.writeCh <- writeRequest{fn: func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT OR IGNORE INTO users (user_key, oaid, mi_id, created_at) VALUES (?, ?, ?, ?)`,
			userKey, oaid, miID, now)
		return err
	}, done: done}

	if err := <-done; err != nil {
		return "", "", err
	}

	err = s.db.QueryRow(`SELECT oaid, mi_id FROM users WHERE user_key = ?`, userKey).Scan(&oaid, &miID)
	if err != nil {
		return "", "", err
	}

	s.userMu.Lock()
	s.users[userKey] = &User{OAID: oaid, MiID: miID}
	s.userMu.Unlock()

	return oaid, miID, nil
}

func (s *Store) GetConversation(userKey, conversationID string) (*Conversation, error) {
	if conversationID == "" {
		conversationID = "default"
	}

	key := fmt.Sprintf("%s|%s", userKey, conversationID)

	s.mu.RLock()
	if conv, ok := s.convs[key]; ok {
		s.mu.RUnlock()
		return conv, nil
	}
	s.mu.RUnlock()

	oaid, miID, err := s.getOrCreateUser(userKey)
	if err != nil {
		return nil, err
	}

	var internalID, historyJSON string
	err = s.db.QueryRow(
		`SELECT internal_conv_id, history_json FROM conversations WHERE user_key = ? AND conversation_id = ?`,
		userKey, conversationID,
	).Scan(&internalID, &historyJSON)

	history := []Message{}
	if err == nil {
		_ = json.Unmarshal([]byte(historyJSON), &history)
	} else if errors.Is(err, sql.ErrNoRows) {
		internalID = newConversationID(oaid)
	} else if err != nil {
		return nil, err
	}

	conv := &Conversation{
		UserKey:        userKey,
		ConversationID: conversationID,
		OAID:           oaid,
		MiID:           miID,
		InternalID:     internalID,
		History:        history,
		LastActive:     time.Now(),
		LastPersist:    time.Now(),
		Dirty:          false,
	}

	s.mu.Lock()
	s.convs[key] = conv
	s.mu.Unlock()

	return conv, nil
}

func (s *Store) Touch(conv *Conversation) {
	conv.mu.Lock()
	conv.LastActive = time.Now()
	conv.mu.Unlock()
}
