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
	"path"
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
	ContentDir  string
	GithubToken string
	Refresh     time.Duration
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
	flag.StringVar(&cfg.Addr, "addr", "127.0.0.1:8080", "listen address")
	flag.StringVar(&cfg.Owner, "owner", "", "github owner/org (required)")
	flag.StringVar(&cfg.Repo, "repo", "", "github repo (required)")
	flag.StringVar(&cfg.Branch, "branch", "main", "branch")
	flag.StringVar(&cfg.ContentDir, "dir", "content", "directory in repo containing markdown")
	flag.StringVar(&cfg.GithubToken, "token", "", "github token (optional, increases rate limits)")
	flag.DurationVar(&cfg.Refresh, "refresh", 2*time.Minute, "refresh interval")
	flag.Parse()

	if cfg.Owner == "" || cfg.Repo == "" {
		log.Fatal("missing -owner or -repo")
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(gmhtml.WithUnsafe()), // 若不需要 md 中的内联 HTML，可删掉
	)

	tpls := template.Must(template.New("all").Parse(pageTemplates))

	cache := &Cache{bySlug: map[string]Post{}}

	// initial sync
	if err := syncOnce(context.Background(), cfg, cache, md); err != nil {
		log.Printf("initial sync error: %v", err)
	}

	// periodic refresh
	go func() {
		t := time.NewTicker(cfg.Refresh)
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
			"RepoLabel": fmt.Sprintf("%s/%s:%s/%s", cfg.Owner, cfg.Repo, cfg.Branch, strings.Trim(cfg.ContentDir, "/")),
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
			"RepoLabel": fmt.Sprintf("%s/%s:%s/%s", cfg.Owner, cfg.Repo, cfg.Branch, strings.Trim(cfg.ContentDir, "/")),
			"Post":      p,
			"LastSync":  lastSync,
			"LastErr":   lastErr,
		}
		render(w, tpls, "post", data)
	})

	log.Printf("listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, securityHeaders(mux)))
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
	Truncated bool `json:"truncated"`
}

type ghContentResp struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

func syncOnce(ctx context.Context, cfg Config, cache *Cache, md goldmark.Markdown) error {
	posts, err := fetchPosts(ctx, cfg, md)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.lastSync = time.Now()
	cache.lastErr = err
	if err == nil {
		cache.posts = posts
		cache.bySlug = map[string]Post{}
		for _, p := range posts {
			cache.bySlug[p.Slug] = p
		}
	}
	return err
}

func fetchPosts(ctx context.Context, cfg Config, md goldmark.Markdown) ([]Post, error) {
	// 1) Repo tree (recursive)
	treeURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", cfg.Owner, cfg.Repo, cfg.Branch)
	var tree ghTreeResp
	if err := ghGetJSON(ctx, cfg, treeURL, &tree); err != nil {
		return nil, fmt.Errorf("fetch tree: %w", err)
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

	// 2) Fetch each markdown file via contents API (simple + stable)
	var posts []Post
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

		posts = append(posts, Post{
			Slug:       slug,
			Title:      title,
			Excerpt:    excerpt,
			HTML:       template.HTML(sb.String()),
			SourcePath: p,
		})
	}

	// 3) Sort (you can replace by commit time if you want)
	sort.Slice(posts, func(i, j int) bool { return posts[i].SourcePath > posts[j].SourcePath })
	return posts, nil
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

var reH1 = regexp.MustCompile(`(?m)^\s*#\s+(.+?)\s*$`)

func extractTitle(md string) string {
	m := reH1.FindStringSubmatch(md)
	if len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func makeExcerpt(md string, n int) string {
	s := md
	// strip fenced code blocks (rough)
	s = regexp.MustCompile("(?s)```.*?```").ReplaceAllString(s, "")
	s = regexp.MustCompile("(?m)^#+\\s*").ReplaceAllString(s, "")
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
	s = regexp.MustCompile(`[^a-zA-Z0-9\-/]+`).ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	s = strings.ReplaceAll(s, "/", "-")
	if s == "" {
		return "post"
	}
	return strings.ToLower(s)
}

// ---------------- templates (HTML+CSS inline, Tailwind CDN) ----------------


const pageTemplates = `
{{define "head"}}
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width,initial-scale=1" />
<script src="https://cdn.tailwindcss.com"></script>
<style>
  /* Markdown 轻量排版 */
  .md h1{font-size:1.6rem;font-weight:800;margin:1.2rem 0 .8rem}
  .md h2{font-size:1.3rem;font-weight:800;margin:1.1rem 0 .7rem}
  .md h3{font-size:1.1rem;font-weight:800;margin:1rem 0 .6rem}
  .md p{margin:.75rem 0;line-height:1.75}
  .md a{text-decoration:underline}
  .md ul{list-style:disc;padding-left:1.5rem;margin:.75rem 0}
  .md ol{list-style:decimal;padding-left:1.5rem;margin:.75rem 0}
  .md blockquote{border-left:3px solid rgba(255,255,255,.18);padding-left:1rem;opacity:.95;margin:1rem 0}
  .md pre{overflow:auto;border-radius:1rem;padding:1rem;background:rgba(0,0,0,.35);border:1px solid rgba(255,255,255,.08);margin:1rem 0}
  .md code{font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:.95em}
  .md table{width:100%;border-collapse:collapse;margin:1rem 0}
  .md th,.md td{border:1px solid rgba(255,255,255,.12);padding:.5rem .6rem}
  .md th{background:rgba(255,255,255,.06);text-align:left}
  .md img{max-width:100%;border-radius:1rem;border:1px solid rgba(255,255,255,.10);margin:1rem 0}
  .md hr{border:0;border-top:1px solid rgba(255,255,255,.10);margin:1.25rem 0}
</style>
{{end}}

{{define "header"}}
<div class="flex items-start justify-between gap-4">
  <a href="/" class="text-xl font-extrabold tracking-tight hover:opacity-90">Home</a>
  <div class="text-right text-xs text-zinc-400 leading-relaxed">
    <div class="truncate max-w-[14rem]">{{.RepoLabel}}</div>
    <div>sync: {{if .LastSync.IsZero}}-{{else}}{{.LastSync.Format "2006-01-02 15:04:05"}}{{end}}</div>
    {{if .LastErr}}<div class="text-amber-300">sync err: {{.LastErr}}</div>{{end}}
  </div>
</div>
{{end}}

{{define "footer"}}
<div class="mt-10 text-xs text-zinc-500">No login. Markdown from GitHub.</div>
{{end}}

{{define "index"}}
<!doctype html>
<html lang="zh-CN">
<head>
  {{template "head" .}}
  <title>首页</title>
</head>
<body class="min-h-screen bg-zinc-950 text-zinc-100">
  <div class="mx-auto max-w-3xl px-4 py-8">
    {{template "header" .}}
    <div class="mt-6 space-y-4">
      {{if not .Posts}}
        <div class="rounded-3xl border border-white/10 bg-white/5 p-6">
          <div class="text-zinc-300">暂无文章：请确认仓库、分支、目录，以及目录下有 .md 文件。</div>
        </div>
      {{end}}
      {{range .Posts}}
        <a href="/p/{{.Slug}}" class="block rounded-3xl border border-white/10 bg-white/5 p-6 hover:bg-white/7 transition">
          <div class="text-lg font-extrabold tracking-tight">{{.Title}}</div>
          <div class="mt-2 text-sm text-zinc-300 leading-relaxed">{{.Excerpt}}</div>
          <div class="mt-4 text-xs text-zinc-500">{{.SourcePath}}</div>
        </a>
      {{end}}
    </div>
    {{template "footer" .}}
  </div>
</body>
</html>
{{end}}

{{define "post"}}
<!doctype html>
<html lang="zh-CN">
<head>
  {{template "head" .}}
  <title>{{.Post.Title}}</title>
</head>
<body class="min-h-screen bg-zinc-950 text-zinc-100">
  <div class="mx-auto max-w-3xl px-4 py-8">
    {{template "header" .}}
    <div class="mt-6">
      <a href="/" class="inline-flex items-center gap-2 text-sm text-zinc-300 hover:text-white">
        <span class="opacity-70">←</span> 返回
      </a>

      <div class="mt-4 rounded-3xl border border-white/10 bg-white/5 p-6">
        <div class="text-2xl font-extrabold tracking-tight">{{.Post.Title}}</div>
        <div class="mt-1 text-xs text-zinc-500">{{.Post.SourcePath}}</div>
        <div class="mt-6 md text-zinc-100">
          {{.Post.HTML}}
        </div>
      </div>
    </div>
    {{template "footer" .}}
  </div>
</body>
</html>
{{end}}
`

