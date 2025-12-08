package main

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gparser "github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
)

const (
	envMarkdownDir     = "BLOG_MARKDOWN_DIR"
	defaultMarkdownDir = "./markdowns"
)

// Post 表示一篇文章
type Post struct {
	Slug    string
	Title   string
	Date    time.Time
	Summary string
	Raw     string
	HTML    template.HTML
}

var (
	tpl         *template.Template
	postsBySlug map[string]*Post
	allPosts    []*Post

	md = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
		),
		goldmark.WithParserOptions(
			gparser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			ghtml.WithHardWraps(),
			ghtml.WithXHTML(),
			ghtml.WithUnsafe(), // markdown 里的原始 HTML 也输出
		),
	)
)

func main() {
	// 1. 加载 markdown
	markdownDir := os.Getenv(envMarkdownDir)
	if markdownDir == "" {
		markdownDir = defaultMarkdownDir
	}

	var err error
	allPosts, postsBySlug, err = loadPosts(markdownDir)
	if err != nil {
		log.Fatalf("加载 markdown 失败: %v", err)
	}

	// 2. 模板
	tpl = template.Must(template.ParseGlob("templates/*.html"))

	// 3. 路由
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/post/", handlePost)

	// 4. 静态图片路由：/images/... -> ./markdowns/images/...
	http.Handle(
		"/images/",
		http.StripPrefix(
			"/images/",
			http.FileServer(http.Dir("markdowns/images")),
		),
	)

	addr := ":8080"
	log.Printf("博客已启动，访问 http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// 首页
func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data := struct {
		Posts []*Post
	}{
		Posts: allPosts,
	}
	if err := tpl.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("渲染 index 失败: %v", err)
	}
}

// 文章页：/post/{slug}
func handlePost(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/post/")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	post, ok := postsBySlug[slug]
	if !ok {
		http.NotFound(w, r)
		return
	}

	data := struct {
		Post *Post
	}{
		Post: post,
	}

	if err := tpl.ExecuteTemplate(w, "post.html", data); err != nil {
		log.Printf("渲染 post 失败: %v", err)
	}
}

// ----------------- 加载 markdown 的辅助函数 -----------------

func loadPosts(root string) ([]*Post, map[string]*Post, error) {
	var posts []*Post
	postsBySlug := make(map[string]*Post)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".markdown") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		raw := string(data)
		title := extractTitle(raw, name)
		slug := makeSlug(path, root)
		summary := makeSummary(raw)

		info, err := d.Info()
		modTime := time.Now()
		if err == nil && info != nil {
			modTime = info.ModTime()
		}

		htmlContent, err := renderMarkdown(raw)
		if err != nil {
			return err
		}

		post := &Post{
			Slug:    slug,
			Title:   title,
			Date:    modTime,
			Summary: summary,
			Raw:     raw,
			HTML:    htmlContent,
		}

		posts = append(posts, post)
		postsBySlug[slug] = post
		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	// 时间倒序
	sort.Slice(posts, func(i, j int) bool {
		return posts[i].Date.After(posts[j].Date)
	})

	return posts, postsBySlug, nil
}

// 标题：取第一行 "# xxx"；没有就用文件名
var titleRegexp = regexp.MustCompile(`(?m)^#\s+(.+)$`)

func extractTitle(content, filename string) string {
	m := titleRegexp.FindStringSubmatch(content)
	if len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	base := filepath.Base(filename)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// slug：相对路径去掉扩展名，斜杠都换成 -
func makeSlug(path, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	slug := strings.ReplaceAll(rel, string(os.PathSeparator), "-")
	slug = strings.ToLower(slug)
	slug = strings.ReplaceAll(slug, " ", "-")
	return slug
}

// 摘要：取前几行正文
func makeSummary(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		b.WriteString(line)
		b.WriteRune(' ')
		if b.Len() > 120 {
			break
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "暂无摘要。"
	}
	runes := []rune(s)
	if len(runes) > 120 {
		s = string(runes[:120]) + "..."
	}
	return s
}

// markdown 渲染成 HTML，并修正图片路径
func renderMarkdown(content string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(content), &buf); err != nil {
		return "", err
	}
	htmlStr := buf.String()

	// 把 markdown 里类似 src="images/xxx.png" / "./images/xxx.png"
	// 统一改成 HTTP 路径 /images/xxx.png
	htmlStr = strings.ReplaceAll(htmlStr, `src="images/`, `src="/images/`)
	htmlStr = strings.ReplaceAll(htmlStr, `src="./images/`, `src="/images/`)

	return template.HTML(htmlStr), nil
}
