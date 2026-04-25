package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"pear2pear/config"

	"github.com/google/uuid"
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
	Type         string   `json:"type"`
	ID           int64    `json:"id,omitempty"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	Payload      any      `json:"payload"`
	MediaType    string   `json:"mediaType"`
	FileName     string   `json:"fileName,omitempty"`
	FileSize    int64    `json:"fileSize,omitempty"`
	MimeType    string   `json:"mimeType,omitempty"`
	ReplyToUser string   `json:"replyToUser,omitempty"`
	ReplyToText string   `json:"replyToText,omitempty"`
	IsRead     bool     `json:"isRead,omitempty"`
	MessageIDs []int64 `json:"messageIds,omitempty"`
}

type Server struct {
	peers           map[string]*Peer
	mu              sync.RWMutex
	db              *sql.DB
	upgrader        websocket.Upgrader
	lastUserRequest map[string]time.Time
	fileStoragePath string
	tokens          map[string]string
}

func NewServer(cfg config.Config, validator *config.OriginValidator, db *sql.DB) *Server {
	return &Server{
		peers:  make(map[string]*Peer),
		db:     db,
		tokens: make(map[string]string),
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
		fileStoragePath: cfg.FileStoragePath,
	}
}

func (s *Server) saveMessage(msg Message) int64 {
	payloadStr, ok := msg.Payload.(string)
	if !ok {
		log.Printf("🔧 Message payload is not string: %T", msg.Payload)
		return 0
	}

	const maxPayloadSize = 1 * 1024 * 1024
	if len(payloadStr) > maxPayloadSize {
		log.Printf("⚠️ Payload too large: %d bytes from %s, limit: %d", len(payloadStr), msg.From, maxPayloadSize)
		return 0
	}

	replyToUser := msg.ReplyToUser
	replyToText := msg.ReplyToText

	result, err := s.db.Exec(
		`INSERT INTO messages (from_user, to_user, payload, media_type, file_name, file_size, mime_type, reply_to_user, reply_to_payload)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.From, msg.To, payloadStr, msg.MediaType, msg.FileName, msg.FileSize, msg.MimeType, replyToUser, replyToText,
	)
	if err != nil {
		log.Println("❌ Ошибка сохранения в БД:", err)
		return 0
	}

	id, err := result.LastInsertId()
	if err != nil {
		log.Printf("🔧 Failed to get last insert ID: %v", err)
		return 0
	}
	return id
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

	token := generateToken()
	s.mu.Lock()
	s.tokens[token] = req.Username
	s.mu.Unlock()

	resp := map[string]string{"username": req.Username, "token": token}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// isRateLimited проверяет и обновляет время последнего запроса от IP.
// Возвращает true, если запрос должен быть отклонён.
// Очищает старые записи старше 1 минуты для предотвращения утечки памяти.
func (s *Server) isRateLimited(ip string) bool {
	if s.lastUserRequest == nil {
		s.lastUserRequest = make(map[string]time.Time)
	}

	// Очистка старых записей each 100 requests to prevent memory leak
	if len(s.lastUserRequest) > 0 && len(s.lastUserRequest)%100 == 0 {
		cutoff := time.Now().Add(-1 * time.Minute)
		for k, v := range s.lastUserRequest {
			if v.Before(cutoff) {
				delete(s.lastUserRequest, k)
			}
		}
	}

	lastRequest, exists := s.lastUserRequest[ip]
	if exists && time.Since(lastRequest) < 5*time.Second {
		return true
	}

	s.lastUserRequest[ip] = time.Now()
	return false
}

func (s *Server) getUserFromToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	username, ok := s.tokens[token]
	return username, ok
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

	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		auth = strings.TrimPrefix(auth, "Bearer ")
	}
	username, ok := s.getUserFromToken(auth)
	if !ok {
		username = ""
	}

	w.Header().Set("Content-Type", "application/json")
	
	unreadMap := make(map[string]int)
	if username != "" {
		rows, err := s.db.Query("SELECT from_user, COUNT(*) FROM messages WHERE to_user = ? AND is_read = 0 GROUP BY from_user", username)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var sender string
				var count int
				if err := rows.Scan(&sender, &count); err == nil {
					unreadMap[sender] = count
				}
			}
		}
	}
	
	log.Printf("📬 handleGetUsers: user=%s, unreadMap=%v", username, unreadMap)
	
	json.NewEncoder(w).Encode(map[string]interface{}{
		"users": usernames,
		"unread": unreadMap,
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

	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		auth = strings.TrimPrefix(auth, "Bearer ")
	}
	username, ok := s.getUserFromToken(auth)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := s.db.Query(
		`SELECT id, from_user, to_user, payload, media_type, file_name, file_size, mime_type, reply_to_user, reply_to_payload, is_read
		 FROM messages
		 WHERE (from_user = ? AND to_user = ?) OR (from_user = ? AND to_user = ?)
		 ORDER BY id ASC LIMIT 50`,
		username, targetUser, targetUser, username,
	)
	if err != nil {
		log.Printf("❌ Failed to query history: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var messages []map[string]interface{}
	var id int64
	for rows.Next() {
		var from, to, payload, mediaType string
		var fileName, mimeType, replyToUser, replyToPayload sql.NullString
		var fileSize int64
		var isRead int
		if err := rows.Scan(&id, &from, &to, &payload, &mediaType, &fileName, &fileSize, &mimeType, &replyToUser, &replyToPayload, &isRead); err != nil {
			log.Printf("🔧 Failed to scan history row: %v", err)
			continue
		}
		msg := map[string]interface{}{
			"id":        id,
			"from":      from,
			"to":        to,
			"payload":   payload,
			"mediaType": mediaType,
			"type":      "chat",
			"isRead":    isRead == 1,
		}
		if fileName.Valid {
			msg["fileName"] = fileName.String
		}
		if fileSize > 0 {
			msg["fileSize"] = fileSize
		}
		if mimeType.Valid {
			msg["mimeType"] = mimeType.String
		}
		if replyToUser.Valid {
			msg["replyToUser"] = replyToUser.String
		}
		if replyToPayload.Valid {
			msg["replyToText"] = replyToPayload.String
		}
		messages = append(messages, msg)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"messages": messages,
	})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	log.Printf("📥 handleUpload: method=%s", r.Method)

	if err := r.ParseMultipartForm(50 << 20); err != nil {
		log.Printf("❌ ParseMultipartForm: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		auth = strings.TrimPrefix(auth, "Bearer ")
	}
	username, ok := s.getUserFromToken(auth)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	toUser := r.FormValue("to_user")
	log.Printf("📥 handleUpload: user=%s, to=%s", username, toUser)
	if toUser == "" {
		http.Error(w, "missing to_user", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileSize := header.Size
	var expiresAt *time.Time

	const (
		MB128 = 128 * 1024 * 1024
		GB1   = 1024 * 1024 * 1024
	)

	if fileSize > GB1 {
		t := time.Now().Add(7 * 24 * time.Hour)
		expiresAt = &t
	} else if fileSize > MB128 {
		t := time.Now().Add(30 * 24 * time.Hour)
		expiresAt = &t
	}

	fileUUID := uuid.New().String()
	filePath := filepath.Join(s.fileStoragePath, fileUUID)

	dst, err := os.Create(filePath)
	if err != nil {
		log.Printf("❌ Ошибка создания файла: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		log.Printf("❌ Ошибка записи файла: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	var expiresAtStr *string
	if expiresAt != nil {
		s := expiresAt.Format("2006-01-02 15:04:05")
		expiresAtStr = &s
	}

	_, err = s.db.Exec(
		`INSERT INTO file_uploads (from_user, to_user, file_uuid, file_path, file_name, file_size, mime_type, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		username, toUser, fileUUID, filePath, header.Filename, fileSize, mimeType, expiresAtStr,
	)
	if err != nil {
		log.Printf("❌ Ошибка сохранения в БД: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	var fileID int64
	err = s.db.QueryRow("SELECT last_insert_rowid()").Scan(&fileID)
	if err != nil {
		log.Printf("⚠️ Не удалось получить file_id: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"file_id":  fileID,
		"url":      "/api/file/" + fileUUID,
		"fileName": header.Filename,
		"fileSize": fileSize,
		"mimeType": mimeType,
	})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		auth = strings.TrimPrefix(auth, "Bearer ")
	}
	// Fallback: try query param if header is empty (for img/video tags)
	if auth == "" {
		auth = r.URL.Query().Get("token")
	}
	username, ok := s.getUserFromToken(auth)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Правильное получение UUID через strings.Split
	path := r.URL.Path
	parts := strings.Split(path, "/api/file/")
	if len(parts) < 2 || parts[1] == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	fileUUID := parts[1]

	// Валидация через uuid.Parse для защиты от Path Traversal
	if _, err := uuid.Parse(fileUUID); err != nil {
		log.Printf("⚠️ Invalid file UUID: %s", fileUUID)
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}

	var (
		id        int64
		fromUser  string
		toUser    string
		filePath  string
		fileName  string
		fileSize  int64
		mimeType  string
		expiresAt sql.NullString
	)
	err := s.db.QueryRow(
		`SELECT id, from_user, to_user, file_path, file_name, file_size, mime_type, expires_at
		 FROM file_uploads WHERE file_uuid = ?`,
		fileUUID,
	).Scan(&id, &fromUser, &toUser, &filePath, &fileName, &fileSize, &mimeType, &expiresAt)

	if err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("❌ Ошибка поиска файла: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if username != fromUser && username != toUser {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if expiresAt.Valid {
		expiresTime, err := time.Parse("2006-01-02 15:04:05", expiresAt.String)
		if err == nil && expiresTime.Before(time.Now()) {
			s.db.Exec("DELETE FROM file_uploads WHERE id = ?", id)
			os.Remove(filePath)
			http.Error(w, "file expired", http.StatusGone)
			return
		}
	}

	info, err := os.Stat(filePath)
	if err != nil {
		log.Printf("❌ Ошибка stat файла: %v", err)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	f, err := os.Open(filePath)
	if err != nil {
		log.Printf("❌ Ошибка открытия файла: %v", err)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", mimeType)

	isMedia := strings.HasPrefix(mimeType, "image/") ||
		strings.HasPrefix(mimeType, "video/") ||
		strings.HasPrefix(mimeType, "audio/")
	if isMedia {
		w.Header().Set("Content-Disposition", "inline; filename=\""+fileName+"\"")
	} else {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+fileName+"\"")
	}

	w.Header().Set("Content-Length", fmt.Sprint(fileSize))

	http.ServeContent(w, r, fileName, info.ModTime(), f)
}

func (s *Server) cleanupExpiredFiles() {
	rows, err := s.db.Query("SELECT id, file_path FROM file_uploads WHERE expires_at IS NOT NULL AND expires_at < datetime('now')")
	if err != nil {
		log.Printf("❌ Ошибка поиска просроченных файлов: %v", err)
		return
	}
	defer rows.Close()

	var count, fileErrs, dbErrs int
	for rows.Next() {
		var id int64
		var filePath string
		if err := rows.Scan(&id, &filePath); err != nil {
			continue
		}
		if err := os.Remove(filePath); err != nil {
			log.Printf("⚠️ Ошибка удаления файла %s: %v", filePath, err)
			fileErrs++
			continue
		}
		if _, err := s.db.Exec("DELETE FROM file_uploads WHERE id = ?", id); err != nil {
			log.Printf("⚠️ Ошибка удаления записи БД id=%d: %v", id, err)
			dbErrs++
			continue
		}
		count++
	}
	if count > 0 {
		log.Printf("🧹 Удалено %d просроченных файлов (ошибок: файлов=%d, БД=%d)", count, fileErrs, dbErrs)
	}
}

func (s *Server) sendHistory(p *Peer) {
	rows, err := s.db.Query(
		`SELECT id, from_user, to_user, payload, media_type, file_name, file_size, mime_type, reply_to_user, reply_to_payload, is_read FROM messages
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
		var fileName, mimeType, replyToUser, replyToPayload sql.NullString
		var fileSize int64
		var isRead int
		err := rows.Scan(&msg.ID, &msg.From, &msg.To, &payloadStr, &msg.MediaType, &fileName, &fileSize, &mimeType, &replyToUser, &replyToPayload, &isRead)
		if err != nil {
			log.Printf("🔧 Failed to scan history row: %v", err)
			continue
		}
		msg.Type = "chat"
		msg.Payload = payloadStr

		if fileName.Valid {
			msg.FileName = fileName.String
		}
		if mimeType.Valid {
			msg.MimeType = mimeType.String
		}
		if replyToUser.Valid {
			msg.ReplyToUser = replyToUser.String
		}
		if replyToPayload.Valid {
			msg.ReplyToText = replyToPayload.String
		}
		msg.FileSize = fileSize
		msg.IsRead = isRead == 1

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
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		auth = strings.TrimPrefix(auth, "Bearer ")
	}
	if auth == "" {
		auth = r.URL.Query().Get("token")
	}
	username, ok := s.getUserFromToken(auth)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WebSocket upgrade failed: %v (Origin: %s)", err, r.Header.Get("Origin"))
		return
	}

	// Set ReadLimit to 2MB to prevent DoS with huge packets
	conn.SetReadLimit(2 << 20)

	// Set up keep-alive with ping/pong
	conn.SetPongHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	// Handle connection - instant reconnect without delay
	// Правильная очередность локов: сначала s.mu, потом peer.mu
	s.mu.Lock()
	var p *Peer
	existingPeer, exists := s.peers[username]
	var oldConn *websocket.Conn
	if exists {
		// Лочим peer.mu перед изменением Conn
		existingPeer.mu.Lock()
		oldConn = existingPeer.Conn
		existingPeer.Conn = conn
		existingPeer.mu.Unlock()
	} else {
		// New peer
		p = &Peer{ID: username, Conn: conn}
		s.peers[username] = p
	}
	s.mu.Unlock()

	// Закрыть старое соединение вне критической секции (после разлочивания)
	// Защитить Close() мьютексом existingPeer.mu
	if oldConn != nil && existingPeer != nil {
		existingPeer.mu.Lock()
		oldConn.Close()
		existingPeer.mu.Unlock()
		log.Printf("🔌 Закрыто старое соединение для пользователя %s", username)
	}

	// Broadcast contacts and send history on every connection
	s.broadcastContacts()

	// Безопасно получить реальный *Peer из мапы
	s.mu.RLock()
	p, exists = s.peers[username]
	s.mu.RUnlock()

	if !exists {
		log.Printf("⚠️ Peer %s не найден в мапе после регистрации, пропускаем отправку истории", username)
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
				p, exists := s.peers[username]
				s.mu.RUnlock()

				if !exists {
					log.Printf("⚠️ Ping ticker: peer %s не найден, завершаем", username)
					return
				}

				p.mu.Lock()
				err := p.Conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second))
				p.mu.Unlock()

				if err != nil {
					log.Printf("⚠️  Ping failed for %s: %v", username, err)
					return
				}
			case <-done:
				return
			}
		}
	}()

	defer func() {
		if p != nil {
			p.mu.Lock()
			conn.Close()
			p.mu.Unlock()
		} else {
			conn.Close()
		}
		s.mu.Lock()
		peer, exists := s.peers[username]
		if exists && peer.Conn == conn {
			delete(s.peers, username)
			s.mu.Unlock()
			s.broadcastContacts()
			log.Printf("👤 Вышел: %s", username)
		} else {
			s.mu.Unlock()
		}
	}()

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("⚠️  WebSocket closed unexpectedly for %s: %v", username, err)
			}
			break
		}

		var msg Message
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			preview := string(msgBytes)
			if len(preview) > 50 {
				preview = preview[:50] + "..."
			}
			log.Printf("🔧 Failed to unmarshal message from %s: %v (raw: %q)", username, err, preview)
			continue
		}
		msg.From = username

		if msg.Type == "chat" {
			newID := s.saveMessage(msg)
			msg.ID = newID

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
		
		if msg.Type == "read_receipt" {
			s.handleReadReceipt(msg.From, msg.MessageIDs)
		}
	}
}

func (s *Server) handleReadReceipt(from string, messageIDs []int64) {
	log.Printf("📬 read_receipt from=%s, ids=%v", from, messageIDs)
	
	if len(messageIDs) == 0 {
		return
	}
	
	placeholders := make([]string, len(messageIDs))
	args := make([]any, len(messageIDs))
	for i, id := range messageIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	
	query := "UPDATE messages SET is_read = 1 WHERE id IN (" + strings.Join(placeholders, ",") + ")"
	result, err := s.db.Exec(query, args...)
	if err != nil {
		log.Printf("🔧 Failed to mark messages as read: %v", err)
		return
	}
	
	rowsAffected, _ := result.RowsAffected()
	log.Printf("📬 Updated %d messages as read", rowsAffected)
	
	args = append([]any{from}, args...)
	placeholders = make([]string, len(messageIDs))
	for i := range messageIDs {
		placeholders[i] = "?"
	}
	
	rows, err := s.db.Query("SELECT id, from_user FROM messages WHERE from_user != ? AND id IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return
	}
	defer rows.Close()
	
	for rows.Next() {
		var msgID int64
		var sender string
		if err := rows.Scan(&msgID, &sender); err != nil {
			continue
		}
		
		s.mu.RLock()
		peer, ok := s.peers[sender]
		s.mu.RUnlock()
		
		if ok {
			peer.mu.Lock()
			peer.Conn.WriteJSON(Message{
				Type:   "read_receipt",
				ID:     msgID,
				IsRead: true,
			})
			peer.mu.Unlock()
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
		file_name TEXT,
		file_size INTEGER DEFAULT 0,
		mime_type TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = db.Exec(messagesQuery)
	if err != nil {
		log.Fatal("Ошибка создания таблицы messages:", err)
	}

	// Добавить колонки если не существуют (миграция для существующей БД)
	// Проверяем существование колонок через PRAGMA для идемпотентности
	var count int
	db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='file_name'").Scan(&count)
	if count == 0 {
		db.Exec("ALTER TABLE messages ADD COLUMN file_name TEXT;")
	}
	db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='file_size'").Scan(&count)
	if count == 0 {
		db.Exec("ALTER TABLE messages ADD COLUMN file_size INTEGER DEFAULT 0;")
	}
	db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='mime_type'").Scan(&count)
	if count == 0 {
		db.Exec("ALTER TABLE messages ADD COLUMN mime_type TEXT;")
	}
	db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='reply_to_user'").Scan(&count)
	if count == 0 {
		db.Exec("ALTER TABLE messages ADD COLUMN reply_to_user TEXT;")
	}
	db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='reply_to_payload'").Scan(&count)
	if count == 0 {
		db.Exec("ALTER TABLE messages ADD COLUMN reply_to_payload TEXT;")
	}
	db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='is_read'").Scan(&count)
	if count == 0 {
		db.Exec("ALTER TABLE messages ADD COLUMN is_read INTEGER DEFAULT 0;")
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

	// Таблица для загрузки файлов
	fileUploadsQuery := `
	CREATE TABLE IF NOT EXISTS file_uploads (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_user TEXT NOT NULL,
		to_user TEXT NOT NULL,
		file_uuid TEXT UNIQUE NOT NULL,
		file_path TEXT NOT NULL,
		file_name TEXT NOT NULL,
		file_size INTEGER NOT NULL,
		mime_type TEXT NOT NULL,
		expires_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = db.Exec(fileUploadsQuery)
	if err != nil {
		log.Fatal("Ошибка создания таблицы file_uploads:", err)
	}

	// Индексы для файлов
	fileIndexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_expires ON file_uploads(expires_at);",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_uuid ON file_uploads(file_uuid);",
	}
	for _, idx := range fileIndexes {
		if _, e := db.Exec(idx); e != nil {
			log.Printf("⚠️ Не удалось создать индекс: %v", e)
		}
	}

	log.Println("✅ Таблица file_uploads готова")

	log.Println("✅ База данных SQLite готова: pear2p.db")
	return db
}

func ensureStorageDir(cfg config.Config) {
	if _, err := os.Stat(cfg.FileStoragePath); os.IsNotExist(err) {
		if err := os.MkdirAll(cfg.FileStoragePath, 0755); err != nil {
			log.Fatal("❌ Не удалось создать директорию хранилища:", err)
		}
		log.Printf("📁 Создана директория хранилища: %s", cfg.FileStoragePath)
	}
	log.Printf("📁 Хранилище файлов: %s", cfg.FileStoragePath)
}

func main() {
	cfg := config.Load()
	ensureStorageDir(cfg)

	validator := config.NewOriginValidator(cfg.AllowedOrigins)
	db := initDB()

	// Закрытие БД при любом выходе (panic, os.Exit, сигнал)
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("⚠️ Ошибка закрытия БД в defer: %v", err)
		}
	}()

	srv := NewServer(cfg, validator, db)

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			srv.cleanupExpiredFiles()
		}
	}()

	http.HandleFunc("/ws", srv.handleWS)
	
	// Debug endpoint
	http.HandleFunc("/debug/messages", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		if user == "" {
			w.Write([]byte("Provide ?user=username"))
			return
		}
		rows, err := srv.db.Query("SELECT id, from_user, to_user, is_read FROM messages WHERE to_user = ? OR from_user = ? ORDER BY id DESC LIMIT 20", user, user)
		if err != nil {
			w.Write([]byte(err.Error()))
			return
		}
		defer rows.Close()
		w.Write([]byte("ID | From | To | is_read\n"))
		for rows.Next() {
			var id, isRead int64
			var from, to string
			rows.Scan(&id, &from, &to, &isRead)
			w.Write([]byte(fmt.Sprintf("%d | %s | %s | %d\n", id, from, to, isRead)))
		}
	})
	
	http.HandleFunc("/api/register", srv.handleRegister)
	http.HandleFunc("/api/login", srv.handleLogin)
	http.HandleFunc("/api/users", srv.handleGetUsers)
	http.HandleFunc("/api/history", srv.handleGetHistory)
	http.HandleFunc("/api/upload", srv.handleUpload)
	http.HandleFunc("/api/file/", srv.handleDownload)
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
