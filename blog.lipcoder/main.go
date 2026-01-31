package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

type Config struct {
	Addr        string
	Owner       string
	Repo        string
	Branch      string
	ContentDir  string // repo directory containing markdown
	GithubToken string  // optional
	DataDir     string  // local cache directory (fixed to ./data)
}

type Post struct {
	Slug       string
	Title      string
	Excerpt    string
	HTML       template.HTML
	SourcePath string
}

type Cache struct {
	mu       sync.RWMutex
	posts    []Post
	bySlug   map[string]Post
	lastSync time.Time
	lastErr  error
}

func main() {
	var cfg Config
	flag.StringVar(&cfg.Addr, "addr", ":8080", "listen address")
	flag.StringVar(&cfg.Owner, "owner", "", "github owner/org (required)")
	flag.StringVar(&cfg.Repo, "repo", "", "github repo (required)")
	flag.StringVar(&cfg.Branch, "branch", "main", "branch")
	flag.StringVar(&cfg.ContentDir, "dir", "data", "directory in repo containing markdown")
	flag.StringVar(&cfg.GithubToken, "token", "", "github token (optional, increases rate limits)")
	flag.Parse()

	if cfg.Owner == "" || cfg.Repo == "" {
		log.Fatal("missing -owner or -repo")
	}

	// local cache under ./data (fixed)
	cfg.DataDir = "data"
	_ = os.MkdirAll(cfg.DataDir, 0o755)

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(gmhtml.WithUnsafe()),
	)

	// templates are separated under ./templates; avoid name collisions by using only:
	// - partials: head/header/footer
	// - pages: index/post
	tpls := template.Must(template.New("").ParseFS(os.DirFS("."), "templates/*.html"))

	cache := &Cache{bySlug: map[string]Post{}}

	// load from disk first (if any)
	if ps, e := loadPostsFromDisk(cfg, md); e == nil && len(ps) > 0 {
		cache.mu.Lock()
		cache.posts = ps
		for _, p := range ps {
			cache.bySlug[p.Slug] = p
		}
		cache.mu.Unlock()
		log.Printf("loaded %d posts from ./data", len(ps))
	}

	// initial GitHub sync
	if err := syncOnce(context.Background(), cfg, cache, md); err != nil {
		log.Printf("initial sync error: %v", err)
	}

	// refresh every hour
	go func() {
		t := time.NewTicker(1 * time.Hour)
		defer t.Stop()
		for range t.C {
			if err := syncOnce(context.Background(), cfg, cache, md); err != nil {
				log.Printf("sync error: %v", err)
			}
		}
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		cache.mu.RLock()
		posts := append([]Post(nil), cache.posts...)
		lastSync := cache.lastSync
		lastErr := cache.lastErr
		cache.mu.RUnlock()

		data := map[string]any{
			"RepoLabel": repoLabel(cfg),
			"Posts":     posts,
			"LastSync":  lastSync,
			"LastErr":   lastErr,
		}
		render(w, tpls, "index", data)
	})

	mux.HandleFunc("/p/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.Trim(strings.TrimPrefix(r.URL.Path, "/p/"), "/")
		if slug == "" {
			http.NotFound(w, r)
			return
		}

		cache.mu.RLock()
		p, ok := cache.bySlug[slug]
		lastSync := cache.lastSync
		lastErr := cache.lastErr
		cache.mu.RUnlock()

		if !ok {
			http.NotFound(w, r)
			return
		}

		data := map[string]any{
			"RepoLabel": repoLabel(cfg),
			"Post":      p,
			"LastSync":  lastSync,
			"LastErr":   lastErr,
		}
		render(w, tpls, "post", data)
	})

	log.Printf("listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, securityHeaders(mux)))
}

func repoLabel(cfg Config) string {
	return fmt.Sprintf("%s/%s:%s/%s", cfg.Owner, cfg.Repo, cfg.Branch, strings.Trim(cfg.ContentDir, "/"))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func render(w http.ResponseWriter, tpls *template.Template, name string, data any) {
	if err := tpls.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// ---------------- GitHub fetch + cache ----------------

type ghTreeResp struct {
	Tree []struct {
		Path string `json:"path"`
		Type string `json:"type"` // blob/tree
	} `json:"tree"`
}

type ghContentResp struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

func syncOnce(ctx context.Context, cfg Config, cache *Cache, md goldmark.Markdown) error {
	posts, rawBySlug, err := fetchPosts(ctx, cfg, md)

	cache.mu.Lock()
	cache.lastSync = time.Now()
	cache.lastErr = err
	// Only replace cache when GitHub fetch is successful AND found posts.
	if err == nil && len(posts) > 0 {
		cache.posts = posts
		cache.bySlug = map[string]Post{}
		for _, p := range posts {
			cache.bySlug[p.Slug] = p
		}
	}
	cache.mu.Unlock()

	if err == nil && len(posts) > 0 {
		if e := savePostsToDisk(cfg, posts, rawBySlug); e != nil {
			log.Printf("save data error: %v", e)
		}
	}
	return err
}

func fetchPosts(ctx context.Context, cfg Config, md goldmark.Markdown) ([]Post, map[string]string, error) {
	treeURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", cfg.Owner, cfg.Repo, cfg.Branch)
	var tree ghTreeResp
	if err := ghGetJSON(ctx, cfg, treeURL, &tree); err != nil {
		return nil, nil, fmt.Errorf("fetch tree: %w", err)
	}

	prefix := strings.Trim(cfg.ContentDir, "/") + "/"
	var mdFiles []string
	for _, it := range tree.Tree {
		if it.Type != "blob" {
			continue
		}
		if !strings.HasPrefix(it.Path, prefix) {
			continue
		}
		if strings.HasSuffix(strings.ToLower(it.Path), ".md") {
			mdFiles = append(mdFiles, it.Path)
		}
	}
	sort.Strings(mdFiles)

	if len(mdFiles) == 0 {
		return nil, nil, fmt.Errorf("no .md files found under repo dir %q (try a different -dir)", cfg.ContentDir)
	}
	log.Printf("found %d markdown files under %q", len(mdFiles), cfg.ContentDir)

	var posts []Post
	rawBySlug := map[string]string{}

	for _, p := range mdFiles {
		body, err := ghFetchFile(ctx, cfg, p)
		if err != nil {
			log.Printf("fetch %s failed: %v", p, err)
			continue
		}

		title := extractTitle(body)
		if title == "" {
			title = strings.TrimSuffix(path.Base(p), path.Ext(p))
		}
		excerpt := makeExcerpt(body, 140)

		var sb strings.Builder
		if err := md.Convert([]byte(body), &sb); err != nil {
			log.Printf("render %s failed: %v", p, err)
			continue
		}

		rel := strings.TrimSuffix(strings.TrimPrefix(p, prefix), ".md")
		slug := slugify(rel)

		rawBySlug[slug] = body

		posts = append(posts, Post{
			Slug:       slug,
			Title:      title,
			Excerpt:    excerpt,
			HTML:       template.HTML(sb.String()),
			SourcePath: p,
		})
	}

	if len(posts) == 0 {
		return nil, nil, fmt.Errorf("markdown files exist but none could be rendered (check logs)")
	}

	sort.Slice(posts, func(i, j int) bool { return posts[i].SourcePath > posts[j].SourcePath })
	return posts, rawBySlug, nil
}

func ghFetchFile(ctx context.Context, cfg Config, filePath string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
		cfg.Owner, cfg.Repo, filePath, cfg.Branch)

	var c ghContentResp
	if err := ghGetJSON(ctx, cfg, url, &c); err != nil {
		return "", err
	}

	if strings.EqualFold(c.Encoding, "base64") {
		dec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(c.Content, "\n", ""))
		if err != nil {
			return "", err
		}
		return string(dec), nil
	}
	return c.Content, nil
}

func ghGetJSON(ctx context.Context, cfg Config, url string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "md-gh-cards")
	if cfg.GithubToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.GithubToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github %d: %s", resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ---------------- helpers ----------------

var (
	reH1          = regexp.MustCompile(`(?m)^\s*#\s+(.+?)\s*$`)
	reStripFences = regexp.MustCompile("(?s)```.*?```")
	reStripHead   = regexp.MustCompile(`(?m)^#+\s*`)
	reSlugBad     = regexp.MustCompile(`[^a-zA-Z0-9\-/]+`)
)

func extractTitle(md string) string {
	m := reH1.FindStringSubmatch(md)
	if len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func makeExcerpt(md string, n int) string {
	s := reStripFences.ReplaceAllString(md, "")
	s = reStripHead.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func slugify(s string) string {
	s = strings.Trim(strings.ReplaceAll(s, "\\", "/"), "/")
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = reSlugBad.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	s = strings.ReplaceAll(s, "/", "-")
	if s == "" {
		return "post"
	}
	return strings.ToLower(s)
}

// ---------------- disk persistence (./data) ----------------

type diskIndex struct {
	GeneratedAt time.Time `json:"generated_at"`
	RepoLabel   string    `json:"repo_label"`
	Posts       []struct {
		Slug       string `json:"slug"`
		Title      string `json:"title"`
		Excerpt    string `json:"excerpt"`
		SourcePath string `json:"source_path"`
	} `json:"posts"`
}

func savePostsToDisk(cfg Config, posts []Post, rawBySlug map[string]string) error {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}

	for _, p := range posts {
		raw, ok := rawBySlug[p.Slug]
		if !ok {
			continue
		}
		fp := filepath.Join(cfg.DataDir, p.Slug+".md")
		if err := os.WriteFile(fp, []byte(raw), 0o644); err != nil {
			return err
		}
	}

	var idx diskIndex
	idx.GeneratedAt = time.Now()
	idx.RepoLabel = repoLabel(cfg)
	for _, p := range posts {
		idx.Posts = append(idx.Posts, struct {
			Slug       string `json:"slug"`
			Title      string `json:"title"`
			Excerpt    string `json:"excerpt"`
			SourcePath string `json:"source_path"`
		}{
			Slug:       p.Slug,
			Title:      p.Title,
			Excerpt:    p.Excerpt,
			SourcePath: p.SourcePath,
		})
	}

	b, _ := json.MarshalIndent(idx, "", "  ")
	tmp := filepath.Join(cfg.DataDir, "index.json.tmp")
	final := filepath.Join(cfg.DataDir, "index.json")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func loadPostsFromDisk(cfg Config, md goldmark.Markdown) ([]Post, error) {
	b, err := os.ReadFile(filepath.Join(cfg.DataDir, "index.json"))
	if err != nil {
		return nil, err
	}
	var idx diskIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, err
	}

	var posts []Post
	for _, it := range idx.Posts {
		raw, err := os.ReadFile(filepath.Join(cfg.DataDir, it.Slug+".md"))
		if err != nil {
			continue
		}
		var sb strings.Builder
		if err := md.Convert(raw, &sb); err != nil {
			continue
		}
		posts = append(posts, Post{
			Slug:       it.Slug,
			Title:      it.Title,
			Excerpt:    it.Excerpt,
			HTML:       template.HTML(sb.String()),
			SourcePath: it.SourcePath,
		})
	}
	return posts, nil
}
