package main

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"
)

const (
	defaultPort = "8080"
	defaultDBPath = "./miui.db"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = defaultDBPath
	}

	store, err := NewStore(dbPath)
	if err != nil {
		panic(err)
	}
	defer store.Close()

	server := NewServer(store, NewMiuiClient())

	mux := http.NewServeMux()
	mux.HandleFunc("/health", methodOnly(http.MethodGet, server.handleHealth))
	mux.HandleFunc("/v1/chat/completions", methodOnly(http.MethodPost, server.handleChatCompletions))
	mux.HandleFunc("/v1/responses", methodOnly(http.MethodPost, server.handleResponses))
	mux.HandleFunc("/v1/messages", methodOnly(http.MethodPost, server.handleClaudeMessages))

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	fmt.Printf("Miui proxy server listening on :%s\n", port)
	if err := httpServer.ListenAndServe(); err != nil {
		panic(err)
	}
}

func methodOnly(method string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		handler(w, r)
	}
}
