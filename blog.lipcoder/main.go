package main

import (
	"bytes"
	"encoding/json"
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
	defaultMarkdownDir = "./data/markdowns"

	commentDir = "./data/comments" // 评论文件存放目录
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

// Comment 表示一条评论
type Comment struct {
	Author     string    `json:"author"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
	GitHubUser string    `json:"github_user,omitempty"` // 绑定的 GitHub 用户名（可空）
}

// 当前登录用户（只在内存里用）
type CurrentUser struct {
	GitHubUser string
	AvatarURL  string
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
			ghtml.WithUnsafe(),
		),
	)
)

func main() {
	// 加载 markdown
	markdownDir := os.Getenv(envMarkdownDir)
	if markdownDir == "" {
		markdownDir = defaultMarkdownDir
	}

	var err error
	allPosts, postsBySlug, err = loadPosts(markdownDir)
	if err != nil {
		log.Fatalf("加载 markdown 失败: %v", err)
	}

	// 解析模板
	tpl = template.Must(template.ParseGlob("templates/*.html"))

	// 路由
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/post/", handlePost)
	http.HandleFunc("/about", handleAbout)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/logout", handleLogout)


	// markdown 图片静态文件：/images/... -> ./markdowns/images/...
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

// 首页：文章列表
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

// 轻量 GitHub 登录：只记用户名到 cookie
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("github_name"))
	next := r.FormValue("next")
	if next == "" {
		next = "/"
	}

	if username == "" {
		// 懒得搞复杂错误码，直接跳回去
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "github_user",
		Value:    username,
		Path:     "/",
		Expires:  time.Now().Add(365 * 24 * time.Hour),
		HttpOnly: true,
	})

	http.Redirect(w, r, next, http.StatusSeeOther)
}

// 注销：清掉 cookie
func handleLogout(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	if next == "" {
		next = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:   "github_user",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}


// 关于页：纯静态介绍
func handleAbout(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/about" {
		http.NotFound(w, r)
		return
	}
	if err := tpl.ExecuteTemplate(w, "about.html", nil); err != nil {
		log.Printf("渲染 about 失败: %v", err)
	}
}

// 文章页 + 评论提交
// 文章页 + 评论提交
// 文章页 + 评论提交
func handlePost(w http.ResponseWriter, r *http.Request) {
	// 解析路径：/post/{slug} 或 /post/{slug}/comment
	path := strings.TrimPrefix(r.URL.Path, "/post/")
	path = strings.Trim(path, "/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(path, "/")
	slug := parts[0]

	post, ok := postsBySlug[slug]
	if !ok {
		http.NotFound(w, r)
		return
	}

	currentUser := currentUserFromRequest(r)

	// 提交评论：/post/{slug}/comment POST
	if len(parts) == 2 && parts[1] == "comment" && r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "表单解析失败", http.StatusBadRequest)
			return
		}
		author := strings.TrimSpace(r.FormValue("author"))
		content := strings.TrimSpace(r.FormValue("content"))

		if content == "" {
			http.Redirect(w, r, "/post/"+slug+"?err=empty", http.StatusSeeOther)
			return
		}

		// 如果已登录 GitHub，则强制使用 GitHub 用户名，且标记 GitHubUser
		c := Comment{
			Author:    author,
			Content:   content,
			CreatedAt: time.Now(),
		}
		if currentUser != nil {
			c.Author = currentUser.GitHubUser
			c.GitHubUser = currentUser.GitHubUser
		} else if c.Author == "" {
			c.Author = "匿名"
		}

		if err := appendComment(slug, c); err != nil {
			log.Printf("写入评论失败: %v", err)
			http.Redirect(w, r, "/post/"+slug+"?err=server", http.StatusSeeOther)
			return
		}

		http.Redirect(w, r, "/post/"+slug, http.StatusSeeOther)
		return
	}

	// 正常 GET 文章页
	comments, _ := loadComments(slug)
	errKey := r.URL.Query().Get("err")
	var errMsg string
	switch errKey {
	case "empty":
		errMsg = "评论内容不能为空。"
	case "server":
		errMsg = "服务器写入失败，请稍后再试。"
	}

	data := struct {
		Post        *Post
		Comments    []Comment
		Error       string
		CurrentUser *CurrentUser
	}{
		Post:        post,
		Comments:    comments,
		Error:       errMsg,
		CurrentUser: currentUser,
	}

	if err := tpl.ExecuteTemplate(w, "post.html", data); err != nil {
		log.Printf("渲染 post 失败: %v", err)
	}
}

// ----------------- markdown 加载 -----------------

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

	sort.Slice(posts, func(i, j int) bool {
		return posts[i].Date.After(posts[j].Date)
	})

	return posts, postsBySlug, nil
}

var titleRegexp = regexp.MustCompile(`(?m)^#\s+(.+)$`)

func extractTitle(content, filename string) string {
	m := titleRegexp.FindStringSubmatch(content)
	if len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	base := filepath.Base(filename)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

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

func renderMarkdown(content string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(content), &buf); err != nil {
		return "", err
	}
	htmlStr := buf.String()

	// 修正图片路径：images/... -> /images/...
	htmlStr = strings.ReplaceAll(htmlStr, `src="images/`, `src="/images/`)
	htmlStr = strings.ReplaceAll(htmlStr, `src="./images/`, `src="/images/`)

	return template.HTML(htmlStr), nil
}

// ----------------- 评论数据持久化 -----------------

func commentsFilePath(slug string) string {
	return filepath.Join(commentDir, slug+".json")
}

func loadComments(slug string) ([]Comment, error) {
	path := commentsFilePath(slug)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []Comment{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cs []Comment
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, err
	}
	return cs, nil
}

func appendComment(slug string, c Comment) error {
	cs, _ := loadComments(slug)
	cs = append(cs, c)

	if err := os.MkdirAll(commentDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(commentsFilePath(slug), data, 0o644)
}

func currentUserFromRequest(r *http.Request) *CurrentUser {
	c, err := r.Cookie("github_user")
	if err != nil {
		return nil
	}
	username := strings.TrimSpace(c.Value)
	if username == "" {
		return nil
	}
	return &CurrentUser{
		GitHubUser: username,
		AvatarURL:  "https://avatars.githubusercontent.com/" + username,
	}
}

