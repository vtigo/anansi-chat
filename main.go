package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	Port          string
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	IdleTimeout   time.Duration
	ShutdownDelay time.Duration
}

type Message struct {
	Username string `json:"username"`
	Message  string `json:"message"`
}

type ChatServer struct {
	upgrader  websocket.Upgrader
	clients   map[*websocket.Conn]bool
	broadcast chan Message
	mutex     sync.Mutex
}

func NewChatServer() *ChatServer {
	return &ChatServer{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		clients:   make(map[*websocket.Conn]bool),
		broadcast: make(chan Message),
	}
}

func (s *ChatServer) Start(ctx context.Context) {
	go s.handleMessages(ctx)
}

func (s *ChatServer) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading to WebSocket: %v", err)
		return
	}
	defer conn.Close()

	s.mutex.Lock()
	s.clients[conn] = true
	s.mutex.Unlock()

	welcomeMsg := Message{
		Username: "System",
		Message:  "Welcome to ANANSI chat!",
	}
	if err := conn.WriteJSON(welcomeMsg); err != nil {
		log.Printf("Error sending welcome message: %v", err)
		s.removeClient(conn)
		return
	}

	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			log.Printf("Error reading message: %v", err)
			s.removeClient(conn)
			return
		}

		log.Printf("Message received: %s from %s", msg.Message, msg.Username)
		s.broadcast <- msg
	}
}

func (s *ChatServer) removeClient(conn *websocket.Conn) {
	s.mutex.Lock()
	delete(s.clients, conn)
	s.mutex.Unlock()
}

func (s *ChatServer) handleMessages(ctx context.Context) {
	for {
		select {
		case msg := <-s.broadcast:
			s.mutex.Lock()
			for client := range s.clients {
				if err := client.WriteJSON(msg); err != nil {
					log.Printf("Error sending message: %v", err)
					client.Close()
					delete(s.clients, client)
				}
			}
			s.mutex.Unlock()
		case <-ctx.Done():
			log.Println("Shutting down message handler")
			return
		}
	}
}

func ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	content, err := os.ReadFile("index.html")
	if err != nil {
		log.Printf("Could not open index.html: %v", err)
		http.Error(w, "Error loading chat interface", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write(content)
}

func main() {
	cfg := Config{
		Port:          ":3333",
		ReadTimeout:   15 * time.Second,
		WriteTimeout:  15 * time.Second,
		IdleTimeout:   60 * time.Second,
		ShutdownDelay: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chatServer := NewChatServer()
	chatServer.Start(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/", ServeHTTP)
	mux.HandleFunc("/ws", chatServer.HandleWebSocket)

	server := &http.Server{
		Addr:         cfg.Port,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	go func() {
		log.Printf("Server started. Visit http://localhost%s in your browser", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop
	log.Println("Shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, cfg.ShutdownDelay)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Error during server shutdown: %v", err)
	}

	cancel()
	log.Println("Server stopped")
}
