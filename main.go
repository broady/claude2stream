package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahimsalabs/durable-streams-go/durablestream"
)

//go:embed webui/dist/*
var webUI embed.FS

func main() {
	addr := flag.String("addr", ":8214", "listen address")
	claudeDir := flag.String("dir", "", "claude directory (default: ~/.claude)")
	dev := flag.Bool("dev", false, "enable CORS for development")
	flag.Parse()

	dir := *claudeDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("get home dir: %v", err)
		}
		dir = filepath.Join(home, ".claude")
	}

	storage, err := NewClaudeStorage(dir)
	if err != nil {
		log.Fatalf("create storage: %v", err)
	}
	defer storage.Close()

	streamHandler := durablestream.NewHandler(storage, nil)

	// Build the main handler
	mux := http.NewServeMux()

	// Serve embedded UI at /ui/
	uiFS, err := fs.Sub(webUI, "webui/dist")
	if err != nil {
		log.Fatalf("embed ui: %v", err)
	}
	mux.Handle("/ui/", http.StripPrefix("/ui/", spaHandler(http.FileServer(http.FS(uiFS)))))

	// Redirect root to UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		// All other paths go to stream handler
		streamHandler.ServeHTTP(w, r)
	})

	var handler http.Handler = mux
	if *dev {
		handler = corsMiddleware(mux)
		log.Printf("CORS enabled for development")
	}

	log.Printf("Claude streams server listening on %s", *addr)
	log.Printf("Watching: %s", dir)
	log.Printf("UI: http://localhost%s/ui/", *addr)

	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// spaHandler wraps a file server to serve index.html for SPA routes
func spaHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := r.URL.Path
		if path == "" || path == "/" {
			path = "index.html"
		}

		// If path has no extension (likely a route), serve index.html
		if !strings.Contains(path, ".") {
			r.URL.Path = "/"
		}

		h.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Stream-Offset, Accept")
		w.Header().Set("Access-Control-Expose-Headers", "Stream-Next-Offset, Stream-Tail-Offset")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
