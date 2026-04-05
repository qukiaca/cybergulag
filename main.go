package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type Peer struct {
	ID   string
	Conn *websocket.Conn
	mu   sync.Mutex // Защита от конкурентных записей в одно соединение
}

type Message struct {
	Type    string `json:"type"`
	From    string `json:"from"`
	To      string `json:"to"`
	Payload any    `json:"payload"`
}

var (
	peers = make(map[string]*Peer)
	mu    sync.RWMutex
)

func (p *Peer) WriteJSON(v any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Conn.WriteJSON(v)
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
		http.Error(w, "missing user_id", 400)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WS upgrade:", err)
		return
	}

	mu.Lock()
	peers[userID] = &Peer{ID: userID, Conn: conn}
	mu.Unlock()
	broadcastContacts()
	log.Printf("🔌 Подключился: %s (всего: %d)", userID, len(peers))

	defer func() {
		mu.Lock()
		delete(peers, userID)
		mu.Unlock()
		conn.Close()
		broadcastContacts()
		log.Printf("🔌 Отключился: %s", userID)
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

		mu.RLock()
		target, ok := peers[msg.To]
		mu.RUnlock()

		if ok {
			target.WriteJSON(msg)
		}
	}
}

func main() {
	http.HandleFunc("/ws", handleWS)
	http.Handle("/", http.FileServer(http.Dir(".")))
	log.Println("🔒 HTTPS сервер: https://192.168.3.38:8080")
	//log.Fatal(http.ListenAndServeTLS(":8080", "cert.pem", "key.pem", nil))
	log.Fatal(http.ListenAndServe(":8080", nil))
}
