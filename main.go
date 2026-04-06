package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite" // Импорт драйвера SQLite
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

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

var (
	peers = make(map[string]*Peer)
	mu    sync.RWMutex
	db    *sql.DB // Наша глобальная переменная для БД
)

// Инициализация базы данных
func initDB() {
	var err error
	db, err = sql.Open("sqlite", "./pear2p.db")
	if err != nil {
		log.Fatal("Ошибка открытия БД:", err)
	}

	// Создаем таблицу, если её нет
	query := `
	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_user TEXT,
		to_user TEXT,
		payload TEXT,
		media_type TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = db.Exec(query)
	if err != nil {
		log.Fatal("Ошибка создания таблицы:", err)
	}
	log.Println("✅ База данных SQLite готова: pear2p.db")
}

// Сохранение сообщения в БД
func saveMessage(msg Message) {
	// Конвертируем Payload в строку (так как там может быть текст или base64)
	payloadStr, ok := msg.Payload.(string)
	if !ok {
		return
	}

	_, err := db.Exec(
		"INSERT INTO messages (from_user, to_user, payload, media_type) VALUES (?, ?, ?, ?)",
		msg.From, msg.To, payloadStr, msg.MediaType,
	)
	if err != nil {
		log.Println("❌ Ошибка сохранения в БД:", err)
	}
}

// Отправка истории сообщений пользователю
func sendHistory(p *Peer) {
	// Берем последние 50 сообщений, где участвует этот пользователь
	rows, err := db.Query(
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
			continue
		}
		msg.Type = "chat"
		msg.Payload = payloadStr

		// Отправляем каждое сообщение из истории пользователю
		p.mu.Lock()
		p.Conn.WriteJSON(msg)
		p.mu.Unlock()
	}
}

func broadcastContacts() {
	mu.RLock()
	users := make([]string, 0, len(peers))
	conns := make([]*Peer, 0, len(peers))
	for _, p := range peers {
		users = append(users, p.ID)
		conns = append(conns, p)
	}
	mu.RUnlock()

	msg := Message{Type: "contacts", Payload: users}
	data, _ := json.Marshal(msg)

	for _, p := range conns {
		p.mu.Lock()
		p.Conn.WriteMessage(websocket.TextMessage, data)
		p.mu.Unlock()
	}
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	p := &Peer{ID: userID, Conn: conn}
	mu.Lock()
	peers[userID] = p
	mu.Unlock()

	// 1. Сначала рассылаем контакты
	broadcastContacts()
	// 2. Затем отправляем историю из БД
	sendHistory(p)

	log.Printf("👤 Вошел: %s", userID)

	defer func() {
		mu.Lock()
		delete(peers, userID)
		mu.Unlock()
		conn.Close()
		broadcastContacts()
		log.Printf("👤 Вышел: %s", userID)
	}()

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if json.Unmarshal(msgBytes, &msg) != nil {
			continue
		}
		msg.From = userID

		// 3. Сохраняем сообщение в БД перед отправкой
		if msg.Type == "chat" {
			saveMessage(msg)
		}

		mu.RLock()
		target, ok := peers[msg.To]
		mu.RUnlock()

		if ok {
			target.mu.Lock()
			target.Conn.WriteJSON(msg)
			target.mu.Unlock()
		}
	}
}

func main() {
	initDB() // Запуск БД
	defer db.Close()

	http.HandleFunc("/ws", handleWS)
	http.Handle("/", http.FileServer(http.Dir(".")))
	log.Println("🚀 Pear2P запущен на http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
