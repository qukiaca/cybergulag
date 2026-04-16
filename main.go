package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"pear2pear/config"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
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
	peers           map[string]*Peer
	mu              sync.RWMutex
	db              *sql.DB
	upgrader        websocket.Upgrader
	lastUserRequest map[string]time.Time
}

func NewServer(cfg config.Config, validator *config.OriginValidator, db *sql.DB) *Server {
	return &Server{
		peers: make(map[string]*Peer),
		db:    db,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if strings.Contains(origin, "trycloudflare.com") {
					return true
				}
				return validator.CheckOrigin(r)
			},
		},
		lastUserRequest: make(map[string]time.Time),
	}
}

func (s *Server) saveMessage(msg Message) {
	payloadStr, ok := msg.Payload.(string)
	if !ok {
		log.Printf("🔧 Message payload is not string: %T", msg.Payload)
		return
	}

	// Проверка размера payload: лимит 1MB
	const maxPayloadSize = 1 * 1024 * 1024 // 1 MB
	if len(payloadStr) > maxPayloadSize {
		log.Printf("⚠️ Payload too large: %d bytes from %s, limit: %d", len(payloadStr), msg.From, maxPayloadSize)
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

	// Cost 12: ~250ms на хеширование — достаточная защита от брутфорса,
	// при этом не создаёт неприемлемую задержку для пользователя при регистрации.
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		log.Printf("❌ Failed to hash password for %s: %v", req.Username, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	_, err = s.db.Exec(
		"INSERT INTO users (username, password) VALUES (?, ?)",
		req.Username, string(hash),
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

	// Сравниваем хеш с введённым паролем через bcrypt
	if err := bcrypt.CompareHashAndPassword([]byte(storedPassword), []byte(req.Password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			// Нормальный случай: неверный пароль
		} else {
			// Ошибка хеша (повреждённые данные, старая запись с plaintext)
			log.Printf("⚠️ bcrypt error for %s: %v", req.Username, err)
		}
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	resp := map[string]string{"username": req.Username}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// isRateLimited проверяет и обновляет время последнего запроса от IP.
// Возвращает true, если запрос должен быть отклонён.
func (s *Server) isRateLimited(ip string) bool {
	if s.lastUserRequest == nil {
		s.lastUserRequest = make(map[string]time.Time)
	}

	lastRequest, exists := s.lastUserRequest[ip]
	if exists && time.Since(lastRequest) < 5*time.Second {
		return true
	}

	s.lastUserRequest[ip] = time.Now()
	return false
}

// Handle get all users (for contacts list) with simple rate limiting
func (s *Server) handleGetUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Simple rate limiting: 1 request per 5 seconds per IP
	// Check for forwarded IP when behind proxy (Cloudflare Tunnel)
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = strings.Split(r.RemoteAddr, ":")[0]
	} else {
		// X-Forwarded-For can contain multiple IPs, take the first one
		if idx := strings.Index(ip, ","); idx != -1 {
			ip = strings.TrimSpace(ip[:idx])
		}
	}

	// Атомарная проверка и обновление лимита под одним локом
	s.mu.Lock()
	limited := s.isRateLimited(ip)
	s.mu.Unlock()

	if limited {
		// Логируем первые 8 байт SHA256 хеша IP — позволяет отслеживать атаки
		// без раскрытия реальных IP адресов в логах
		hash := sha256.Sum256([]byte(ip))
		log.Printf("⚠️ Rate limit: IP=%x", hash[:8])
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	// Get all usernames from users table
	rows, err := s.db.Query("SELECT username FROM users ORDER BY username")
	if err != nil {
		log.Printf("❌ Failed to query users: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var usernames []string
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			log.Printf("🔧 Failed to scan username: %v", err)
			continue
		}
		usernames = append(usernames, username)
	}
	if err := rows.Err(); err != nil {
		log.Printf("❌ Error iterating users: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"users": usernames,
	})
}

// Handle get message history between current user and target user
func (s *Server) handleGetHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targetUser := r.URL.Query().Get("with")
	if targetUser == "" {
		http.Error(w, "missing 'with' parameter", http.StatusBadRequest)
		return
	}

	// Получаем user_id из WebSocket (через query param или cookie)
	// В этом эндпоинте используем заголовок X-User-ID (устанавливается фронтендом)
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Запрос сообщений между двумя пользователями
	rows, err := s.db.Query(
		`SELECT from_user, to_user, payload, media_type FROM messages 
		 WHERE (from_user = ? AND to_user = ?) OR (from_user = ? AND to_user = ?)
		 ORDER BY id ASC LIMIT 50`,
		userID, targetUser, targetUser, userID,
	)
	if err != nil {
		log.Printf("❌ Failed to query history: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var payloadStr string
		if err := rows.Scan(&msg.From, &msg.To, &payloadStr, &msg.MediaType); err != nil {
			log.Printf("🔧 Failed to scan history row: %v", err)
			continue
		}
		msg.Type = "chat"
		msg.Payload = payloadStr
		messages = append(messages, msg)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"messages": messages,
	})
}

func (s *Server) sendHistory(p *Peer) {
	rows, err := s.db.Query(
		`SELECT from_user, to_user, payload, media_type FROM messages 
		 WHERE from_user = ? OR to_user = ? 
		 ORDER BY id ASC LIMIT 50`,
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
		log.Printf("❌ WebSocket upgrade failed: %v (Origin: %s)", err, r.Header.Get("Origin"))
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
	var oldConn *websocket.Conn
	if exists {
		// Сохранить старое соединение для закрытия вне критической секции
		existingPeer.mu.Lock()
		oldConn = existingPeer.Conn
		existingPeer.Conn = conn
		existingPeer.mu.Unlock()
	} else {
		// New peer
		p := &Peer{ID: userID, Conn: conn}
		s.peers[userID] = p
	}
	s.mu.Unlock()

	// Закрыть старое соединение вне критической секции (не держать s.mu)
	if oldConn != nil {
		oldConn.Close()
		log.Printf("🔌 Закрыто старое соединение для пользователя %s", userID)
	}

	// Broadcast contacts and send history on every connection
	s.broadcastContacts()

	// Безопасно получить реальный *Peer из мапы
	s.mu.RLock()
	p, exists := s.peers[userID]
	s.mu.RUnlock()

	if !exists {
		log.Printf("⚠️ Peer %s не найден в мапе после регистрации, пропускаем отправку истории", userID)
	} else {
		s.sendHistory(p)
	}
	log.Printf("📊 Текущее количество онлайн-пользователей: %d", len(s.peers))

	// Start ping ticker
	// Важно: WriteControl должен быть защищён тем же мьютексом, что и WriteMessage/WriteJSON,
	// иначе возможна concurrency panic при одновременной записи в сокет с других горутин.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(50 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Безопасно получить пира для доступа к защищённому соединению
				s.mu.RLock()
				p, exists := s.peers[userID]
				s.mu.RUnlock()

				if !exists {
					log.Printf("⚠️ Ping ticker: peer %s не найден, завершаем", userID)
					return
				}

				p.mu.Lock()
				err := p.Conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second))
				p.mu.Unlock()

				if err != nil {
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
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			preview := string(msgBytes)
			if len(preview) > 50 {
				preview = preview[:50] + "..."
			}
			log.Printf("🔧 Failed to unmarshal message from %s: %v (raw: %q)", userID, err, preview)
			continue
		}
		msg.From = userID

		if msg.Type == "chat" {
			// Save message to persistent storage
			s.saveMessage(msg)

			// Check if recipient is online
			s.mu.RLock()
			target, ok := s.peers[msg.To]
			s.mu.RUnlock()

			if ok {
				// Recipient is online - deliver via WebSocket
				target.mu.Lock()
				err := target.Conn.WriteJSON(msg)
				if err != nil {
					log.Printf("⚠️  Failed to send message to %s: %v", target.ID, err)
				}
				target.mu.Unlock()
			}
			// If offline - message is saved, recipient will get it from history on next connect
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

	// Создание индексов для оптимизации поиска истории сообщений
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_messages_from ON messages(from_user);",
		"CREATE INDEX IF NOT EXISTS idx_messages_to ON messages(to_user);",
		"CREATE INDEX IF NOT EXISTS idx_messages_pair ON messages(from_user, to_user);",
	}
	for _, idx := range indexes {
		if _, err := db.Exec(idx); err != nil {
			log.Printf("⚠️ Не удалось создать индекс: %v", err)
		}
	}

	log.Println("✅ База данных SQLite готова: pear2p.db")
	return db
}

func main() {
	cfg := config.Load()
	validator := config.NewOriginValidator(cfg.AllowedOrigins)
	db := initDB()

	// Закрытие БД при любом выходе (panic, os.Exit, сигнал)
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("⚠️ Ошибка закрытия БД в defer: %v", err)
		}
	}()

	srv := NewServer(cfg, validator, db)

	http.HandleFunc("/ws", srv.handleWS)
	http.HandleFunc("/api/register", srv.handleRegister)
	http.HandleFunc("/api/login", srv.handleLogin)
	http.HandleFunc("/api/users", srv.handleGetUsers)
	http.HandleFunc("/api/history", srv.handleGetHistory)
	http.Handle("/", http.FileServer(http.Dir(".")))

	// Создаём HTTP сервер для graceful shutdown
	httpSrv := &http.Server{
		Addr: ":" + cfg.Port,
	}

	// Контекст для graceful shutdown с таймаутом 30 секунд
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Канал для получения сигналов SIGINT/SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Горутина для обработки сигналов
	go func() {
		sig := <-sigChan
		log.Printf("📡 Получен сигнал %v, начинаем graceful shutdown...", sig)

		// Запрашиваем graceful shutdown HTTP сервера
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Printf("⚠️ Ошибка HTTP shutdown: %v", err)
		}

		// Закрываем все активные WebSocket соединения
		srv.mu.Lock()
		connCount := len(srv.peers)
		for _, p := range srv.peers {
			p.mu.Lock()
			if p.Conn != nil {
				p.Conn.Close()
			}
			p.mu.Unlock()
		}
		srv.mu.Unlock()
		log.Printf("🛑 Сервер остановлен, активных соединений: %d", connCount)

		// Закрываем соединение с БД
		// SQLite безопасен при двойном закрытии, defer в main() обработает panic/os.Exit
		if err := db.Close(); err != nil {
			log.Printf("⚠️ Ошибка закрытия БД: %v", err)
		}
	}()

	log.Printf("🚀 Pear2P запущен на http://localhost:%s", cfg.Port)
	log.Printf("🔒 Allowed origins: %s", validator)

	// Запуск сервера
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("❌ Ошибка сервера: %v", err)
	}

	// Ждём завершения shutdown
	<-ctx.Done()
	log.Println("👋 Сервер завершён")
}
