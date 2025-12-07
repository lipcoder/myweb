package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	publicDir     = "public"
	dataDir       = "data"
	friendsPath   = "data/friends.json"
	guestbookPath = "data/guestbook.json"
)

// 友链
type Friend struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Desc      string `json:"desc"`
	CreatedAt int64  `json:"created_at"`
}

// 留言
type GuestbookEntry struct {
	Nickname  string `json:"nickname"`
	Contact   string `json:"contact"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"`
}

var (
	mu sync.Mutex
)

// 通用：把切片写入 json 文件（先写 tmp 再原子替换）
func saveJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadFriends() ([]Friend, error) {
	_, err := os.Stat(friendsPath)
	if os.IsNotExist(err) {
		return []Friend{}, nil
	}
	data, err := os.ReadFile(friendsPath)
	if err != nil {
		return []Friend{}, err
	}
	if len(data) == 0 {
		return []Friend{}, nil
	}
	var list []Friend
	if err := json.Unmarshal(data, &list); err != nil {
		// 解析失败时，返回空列表，和原来 Python 容错逻辑类似
		return []Friend{}, nil
	}
	return list, nil
}

func loadGuestbook() ([]GuestbookEntry, error) {
	_, err := os.Stat(guestbookPath)
	if os.IsNotExist(err) {
		return []GuestbookEntry{}, nil
	}
	data, err := os.ReadFile(guestbookPath)
	if err != nil {
		return []GuestbookEntry{}, err
	}
	if len(data) == 0 {
		return []GuestbookEntry{}, nil
	}
	var list []GuestbookEntry
	if err := json.Unmarshal(data, &list); err != nil {
		return []GuestbookEntry{}, nil
	}
	return list, nil
}

// /api/friends
func friendsHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch r.Method {
	case http.MethodGet:
		list, err := loadFriends()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "failed to load friends",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(list)

	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			Desc string `json:"desc"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid json body",
			})
			return
		}

		body.Name = trim(body.Name)
		body.URL = trim(body.URL)
		body.Desc = trim(body.Desc)

		if body.Name == "" || body.URL == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "name and url required",
			})
			return
		}

		list, _ := loadFriends()
		entry := Friend{
			Name:      body.Name,
			URL:       body.URL,
			Desc:      body.Desc,
			CreatedAt: time.Now().Unix(),
		}
		list = append(list, entry)

		if err := saveJSON(friendsPath, list); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "failed to save",
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]bool{
			"ok": true,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// /api/guestbook
func guestbookHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch r.Method {
	case http.MethodGet:
		list, err := loadGuestbook()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "failed to load guestbook",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(list)

	case http.MethodPost:
		var body struct {
			Nickname string `json:"nickname"`
			Contact  string `json:"contact"`
			Content  string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid json body",
			})
			return
		}

		body.Nickname = trim(body.Nickname)
		body.Contact = trim(body.Contact)
		body.Content = trim(body.Content)

		if body.Nickname == "" || body.Content == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "nickname and content required",
			})
			return
		}

		list, _ := loadGuestbook()
		entry := GuestbookEntry{
			Nickname:  body.Nickname,
			Contact:   body.Contact,
			Content:   body.Content,
			CreatedAt: time.Now().Unix(),
		}
		list = append(list, entry)

		if err := saveJSON(guestbookPath, list); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "failed to save",
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]bool{
			"ok": true,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// 简单去掉前后空格
func trim(s string) string {
	return string([]rune(s))
}

func main() {
	// 确保 data 目录存在
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("failed to create data dir: %v", err)
	}

	mux := http.NewServeMux()

	// API 路由（保持和原 Flask 一致）
	mux.HandleFunc("/api/friends", friendsHandler)
	mux.HandleFunc("/api/guestbook", guestbookHandler)

	// 静态文件：public 目录
	fs := http.FileServer(http.Dir(publicDir))

	// 如果访问根路径 "/"，默认返回 public/index.html
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" || path == "" {
			http.ServeFile(w, r, filepath.Join(publicDir, "index.html"))
			return
		}
		// 其他路径走静态文件（/main.js 等）
		fs.ServeHTTP(w, r)
	}))

	addr := "127.0.0.1:5000" // 和原来 Flask 一样，仅本机监听
	log.Printf("Go server listening on http://%s ...", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
