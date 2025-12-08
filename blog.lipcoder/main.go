package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// 文章目录，可以改成环境变量 BLOG_MARKDOWN_DIR 覆盖
	envMarkdownDir     = "BLOG_MARKDOWN_DIR"
	defaultMarkdownDir = "./markdowns"
)

// Post 表示一篇文章
type Post struct {
	Slug        string
	Title       string
	Date        time.Time
	Summary     string
	Raw         string          // 原始 markdown 文本
	HTML        template.HTML   // 渲染后的 HTML
	ReadingTime int             // 估算阅读时长（分钟）
}

var (
	tpl         *template.Template
	postsBySlug map[string]*Post
	allPosts    []*Post
)

func main() {
	// 1. 加载文章
	markdownDir := os.Getenv(envMarkdownDir)
	if markdownDir == "" {
		markdownDir = defaultMarkdownDir
	}

	var err error
	allPosts, postsBySlug, err = loadPosts(markdownDir)
	if err != nil {
		log.Fatalf("加载 markdown 失败: %v", err)
	}

	// 2. 解析 templates 目录下所有 html
	tpl = template.Must(template.ParseGlob("templates/*.html"))

	// 3. 路由
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/post/", handlePost)

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

		info, _ := d.Info()
		modTime := time.Now()
		if info != nil {
			modTime = info.ModTime()
		}

		html := markdownToHTML(raw)
		readingTime := calcReadingTime(raw)

		post := &Post{
			Slug:        slug,
			Title:       title,
			Date:        modTime,
			Summary:     summary,
			Raw:         raw,
			HTML:        html,
			ReadingTime: readingTime,
		}

		posts = append(posts, post)
		postsBySlug[slug] = post
		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	// 按时间倒序
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

// 摘要：取前几行正文拼起来
func makeSummary(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") { // 跳过标题
			continue
		}
		b.WriteString(line)
		b.WriteRune(' ')
		if b.Len() > 80 {
			break
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "暂无摘要。"
	}
	runes := []rune(s)
	if len(runes) > 80 {
		s = string(runes[:80]) + "..."
	}
	return s
}

// 简易 markdown -> HTML（支持标题、段落、代码块）
func markdownToHTML(md string) template.HTML {
	lines := strings.Split(md, "\n")
	var b strings.Builder
	inCode := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 代码块 ``` 包裹
		if strings.HasPrefix(trimmed, "```") {
			if !inCode {
				b.WriteString("<pre><code>")
				inCode = true
			} else {
				b.WriteString("</code></pre>\n")
				inCode = false
			}
			continue
		}

		if inCode {
			// 代码内容需要转义
			template.HTMLEscape(&b, []byte(line+"\n"))
			continue
		}

		if trimmed == "" {
			continue
		}

		// 标题 # / ## / ... / ######
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level > 6 {
				level = 6
			}
			text := strings.TrimSpace(trimmed[level:])
			if text == "" {
				continue
			}
			tag := fmt.Sprintf("h%d", level)
			b.WriteString("<" + tag + ">")
			template.HTMLEscape(&b, []byte(text))
			b.WriteString("</" + tag + ">\n")
			continue
		}

		// 普通段落
		b.WriteString("<p>")
		template.HTMLEscape(&b, []byte(trimmed))
		b.WriteString("</p>\n")
	}

	return template.HTML(b.String())
}

// 根据字数估算阅读时长（分钟）
func calcReadingTime(content string) int {
	// 简单按空白分词
	words := strings.Fields(content)
	const wpm = 200 // words per minute
	minutes := (len(words) + wpm - 1) / wpm
	if minutes <= 0 {
		minutes = 1
	}
	return minutes
}
