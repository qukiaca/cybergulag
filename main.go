package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"pear2pear/config"

	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"
)

type Peer struct {
	ID   string
	Conn *websocket.Conn
	mu   sync.Mutex
}

type Message struct {
	Type      string `json:"type"`
	From      string `json:"from"`
	To        string `json:"to"`
	Payload   any    `json:"payload"`
	MediaType string `json:"mediaType"`
}

type Server struct {
	peers    map[string]*Peer
	mu       sync.RWMutex
	db       *sql.DB
	upgrader websocket.Upgrader
}

func NewServer(cfg config.Config, validator *config.OriginValidator, db *sql.DB) *Server {
	return &Server{
		peers: make(map[string]*Peer),
		db:    db,
		upgrader: websocket.Upgrader{
			CheckOrigin: validator.CheckOrigin,
		},
	}
}

func (s *Server) saveMessage(msg Message) {
	payloadStr, ok := msg.Payload.(string)
	if !ok {
		log.Printf("🔧 Message payload is not string: %T", msg.Payload)
		return
	}

	_, err := s.db.Exec(
		"INSERT INTO messages (from_user, to_user, payload, media_type) VALUES (?, ?, ?, ?)",
		msg.From, msg.To, payloadStr, msg.MediaType,
	)
	if err != nil {
		log.Println("❌ Ошибка сохранения в БД:", err)
	}
}

// Handle user registration
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}

	// TODO: switch to bcrypt hashing in production
	_, err := s.db.Exec(
		"INSERT INTO users (username, password) VALUES (?, ?)",
		req.Username, req.Password,
	)
	if err != nil {
		// Check if it's a uniqueness constraint violation
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			http.Error(w, "username already exists", http.StatusConflict)
			return
		}
		log.Printf("❌ Failed to register user %s: %v", req.Username, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := map[string]string{"username": req.Username}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Handle user login
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}

	var storedPassword string
	err := s.db.QueryRow(
		"SELECT password FROM users WHERE username = ?",
		req.Username,
	).Scan(&storedPassword)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		log.Printf("❌ Failed to query user %s: %v", req.Username, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// TODO: switch to bcrypt hashing in production
	if storedPassword != req.Password {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	resp := map[string]string{"username": req.Username}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) sendHistory(p *Peer) {
	rows, err := s.db.Query(
		`SELECT from_user, to_user, payload, media_type FROM messages 
		 WHERE from_user = ? OR to_user = ? 
		 ORDER BY created_at ASC LIMIT 50`,
		p.ID, p.ID,
	)
	if err != nil {
		log.Println("Ошибка чтения истории:", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var msg Message
		var payloadStr string
		err := rows.Scan(&msg.From, &msg.To, &payloadStr, &msg.MediaType)
		if err != nil {
			log.Printf("🔧 Failed to scan history row: %v", err)
			continue
		}
		msg.Type = "chat"
		msg.Payload = payloadStr

		p.mu.Lock()
		err = p.Conn.WriteJSON(msg)
		p.mu.Unlock()
		if err != nil {
			log.Printf("⚠️  Failed to send history to %s: %v", p.ID, err)
		}
	}
}

func (s *Server) broadcastContacts() {
	s.mu.RLock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, p)
	}
	s.mu.RUnlock()

	msg := Message{Type: "contacts"}
	var ids []string
	for _, p := range peers {
		ids = append(ids, p.ID)
	}
	msg.Payload = ids
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("❌ Failed to marshal contacts: %v", err)
		return
	}

	for _, p := range peers {
		p.mu.Lock()
		err := p.Conn.WriteMessage(websocket.TextMessage, data)
		p.mu.Unlock()
		if err != nil {
			log.Printf("⚠️  Failed to broadcast contacts to %s: %v", p.ID, err)
			// Note: We don't remove the peer here to avoid holding s.mu
			// Peer cleanup happens in handleWS defer
		}
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		http.Error(w, "missing user_id", http.StatusBadRequest)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	// Set up keep-alive with ping/pong
	conn.SetPongHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	// Handle connection - instant reconnect without delay
	s.mu.Lock()
	existingPeer, exists := s.peers[userID]
	var isReconnect bool
	if exists {
		isReconnect = true
		// Update connection for existing peer
		existingPeer.mu.Lock()
		existingPeer.Conn = conn
		existingPeer.mu.Unlock()
	} else {
		// New peer
		p := &Peer{ID: userID, Conn: conn}
		s.peers[userID] = p
		isReconnect = false
	}
	s.mu.Unlock()

	// Only broadcast contacts and send history for new connections
	if !isReconnect {
		s.broadcastContacts()
		s.sendHistory(&Peer{ID: userID, Conn: conn}) // Temporary peer for history
		log.Printf("👤 Вошел: %s", userID)
	} else {
		log.Printf("🔁 Переподключение: %s", userID)
	}

	// Start ping ticker
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(50 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
					log.Printf("⚠️  Ping failed for %s: %v", userID, err)
					return
				}
			case <-done:
				return
			}
		}
	}()

	defer func() {
		conn.Close()
		s.mu.Lock()
		peer, exists := s.peers[userID]
		if exists && peer.Conn == conn {
			delete(s.peers, userID)
			s.mu.Unlock()
			s.broadcastContacts()
			log.Printf("👤 Вышел: %s", userID)
		} else {
			s.mu.Unlock()
		}
	}()

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("⚠️  WebSocket closed unexpectedly for %s: %v", userID, err)
			}
			break
		}

		var msg Message
		if json.Unmarshal(msgBytes, &msg) != nil {
			log.Printf("🔧 Failed to unmarshal message from %s: %v", userID, err)
			continue
		}
		msg.From = userID

		if msg.Type == "chat" {
			s.saveMessage(msg)
		}

		s.mu.RLock()
		target, ok := s.peers[msg.To]
		s.mu.RUnlock()

		if ok {
			target.mu.Lock()
			err := target.Conn.WriteJSON(msg)
			if err != nil {
				log.Printf("⚠️  Failed to send message to %s: %v", target.ID, err)
			}
			target.mu.Unlock()
		}
	}
}

func initDB() *sql.DB {
	db, err := sql.Open("sqlite", "./pear2p.db")
	if err != nil {
		log.Fatal("Ошибка открытия БД:", err)
	}

	// Create messages table
	messagesQuery := `
	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_user TEXT,
		to_user TEXT,
		payload TEXT,
		media_type TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = db.Exec(messagesQuery)
	if err != nil {
		log.Fatal("Ошибка создания таблицы messages:", err)
	}

	// Create users table for authentication
	usersQuery := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = db.Exec(usersQuery)
	if err != nil {
		log.Fatal("Ошибка создания таблицы users:", err)
	}
	// TODO: switch to bcrypt hashing in production

	log.Println("✅ База данных SQLite готова: pear2p.db")
	return db
}

func main() {
	cfg := config.Load()
	validator := config.NewOriginValidator(cfg.AllowedOrigins)
	db := initDB()
	defer db.Close()

	srv := NewServer(cfg, validator, db)

	http.HandleFunc("/ws", srv.handleWS)
	http.HandleFunc("/api/register", srv.handleRegister)
	http.HandleFunc("/api/login", srv.handleLogin)
	http.Handle("/", http.FileServer(http.Dir(".")))

	log.Printf("🚀 Pear2P запущен на http://localhost:%s", cfg.Port)
	log.Printf("🔒 Allowed origins: %s", validator)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, nil))
}
