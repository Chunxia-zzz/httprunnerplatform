// Package webui 把前端构建产物内嵌进可执行文件，实现单文件交付。
//
// 方案 9.1 的交付形态是「一个 exe 丢过去就能跑」。做法是 go:embed 把
// web/dist 的内容编进二进制，由 gin 在 NoRoute 里兜底：
//
//	GET /assets/index-xxxx.js   → 内嵌的静态文件（带长缓存）
//	GET /runs/8                 → 内嵌的 index.html（SPA fallback）
//	GET /api/v1/不存在           → 仍然是 JSON 兜底错误，**不落进 SPA fallback**
//
// 最后一条是刻意的：接口路径写错时若返回一坨 HTML，前端只会得到
// 「Unexpected token '<'」，排查成本极高。API 前缀必须始终走 JSON。
//
// 内嵌目录里只提交一个 .gitkeep 占位（见 .gitignore）。这样全新克隆
// 不装 Node 也能 go build / go test，只是启动后访问到的是"前端未构建"
// 提示页 —— 明确告知，而不是给一个空白页让人以为是白屏 bug。
package webui

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// distDir 必须与 scripts/build.sh 里同步的目标目录一致。
//
//go:embed all:dist
var embedded embed.FS

const (
	distDir   = "dist"
	indexName = "index.html"
	// buildIDName 由构建脚本写入，内容是构建时间。启动时打出来，
	// 用于判断"手里这个 exe 到底带的是哪一版前端"——没有它就只能靠猜。
	buildIDName = "build-id"
)

// UI 是内嵌前端资源的只读视图。
type UI struct {
	fsys      fs.FS
	index     []byte // 已构建时是真实 index.html，否则是提示页
	available bool   // 是否真的内嵌了构建产物
	fileCount int
	buildID   string
}

// Load 从二进制内嵌资源构造 UI。
func Load() (*UI, error) {
	sub, err := fs.Sub(embedded, distDir)
	if err != nil {
		// 只有在 embed 指令与目录名不一致时才会走到这里，属编译期问题
		return nil, fmt.Errorf("定位内嵌前端目录 %q 失败: %w", distDir, err)
	}
	return New(sub)
}

// New 从任意 fs.FS 构造 UI。
//
// 之所以导出：一是单测要注入 fstest.MapFS 覆盖"资源缺失/路径穿越"
// 这类分支；二是将来若要做「从磁盘目录托管前端以便不重新编译就换界面」，
// 直接传 os.DirFS 即可，不必改这个包。
func New(fsys fs.FS) (*UI, error) {
	u := &UI{fsys: fsys}

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			u.fileCount++
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("遍历内嵌前端失败: %w", err)
	}

	if b, err := fs.ReadFile(fsys, buildIDName); err == nil {
		u.buildID = strings.TrimSpace(string(b))
	}

	idx, err := fs.ReadFile(fsys, indexName)
	if err != nil {
		// 没构建过 —— 不是错误，降级到提示页。
		u.available = false
		u.index = []byte(notBuiltPage)
		return u, nil
	}

	u.available = true
	u.index = idx
	return u, nil
}

// Available 表示是否内嵌了真实构建产物。
func (u *UI) Available() bool { return u.available }

// FileCount 是内嵌的文件总数（含未构建时的占位文件）。
func (u *UI) FileCount() int { return u.fileCount }

// BuildID 是构建脚本写入的时间戳；未构建时为空。
func (u *UI) BuildID() string { return u.buildID }

// Describe 返回一行用于启动日志的描述。
func (u *UI) Describe() string {
	if !u.available {
		return "未内嵌前端构建产物（服务启动后访问 / 会看到构建提示页）"
	}
	if u.buildID != "" {
		return fmt.Sprintf("已内嵌前端 %d 个文件，构建于 %s", u.fileCount, u.buildID)
	}
	return fmt.Sprintf("已内嵌前端 %d 个文件", u.fileCount)
}

// Handler 返回处理静态资源与 SPA fallback 的 gin 处理器。
//
// 只应在 NoRoute 里调用 —— 它假定"没有任何 API 路由匹配"。
func (u *UI) Handler() gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(u.fsys))

	return func(c *gin.Context) {
		// path.Clean 会把 //、/./ 、/../ 归一化；TrimPrefix 后得到
		// FS 内的相对路径（与 fs.ValidPath 的语义一致）。
		p := strings.TrimPrefix(path.Clean(c.Request.URL.Path), "/")
		if p == "." || p == "" {
			u.serveIndex(c)
			return
		}

		if u.isFile(p) {
			if strings.HasPrefix(p, "assets/") {
				// Vite 产出的文件名带内容哈希，可以放心长缓存。
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		// ⭐ 带扩展名却找不到的，必须真 404。
		//
		// 否则 /assets/index-旧哈希.js 会拿到 index.html，浏览器报
		// 「Unexpected token '<'」——错误信息与实际原因（前端换了版本、
		// 浏览器缓存了旧 HTML）相隔十万八千里。
		if ext := path.Ext(p); ext != "" && ext != ".html" {
			c.Status(http.StatusNotFound)
			c.Writer.WriteString("资源不存在: " + p) //nolint:errcheck // 404 响应体
			return
		}

		// 其余（/、/runs/8、/cases/new …）是前端路由，交给 index.html。
		u.serveIndex(c)
	}
}

func (u *UI) isFile(p string) bool {
	f, err := u.fsys.Open(p)
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck // 只做存在性判断
	st, err := f.Stat()
	return err == nil && !st.IsDir()
}

func (u *UI) serveIndex(c *gin.Context) {
	if !u.available {
		// 提示页不进缓存：一旦用户构建完，刷新即可看到真实界面。
		c.Header("Cache-Control", "no-store")
	} else {
		// index.html 里写着带哈希的资源名，缓存它会让新版本"刷新不出来"。
		c.Header("Cache-Control", "no-cache")
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", u.index)
}

// notBuiltPage 是"后端跑起来了但前端没构建"时展示的页面。
//
// 刻意做成页面而不是 404 或 JSON：使用者是浏览器，需要的是
// 「现在该干什么」，而不是一个错误码。
const notBuiltPage = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>前端未构建 · httprunnerplatform</title>
<style>
  body { margin:0; padding:48px 24px; font: 14px/1.7 -apple-system, "Segoe UI", "Microsoft YaHei", sans-serif; color:#1f2328; background:#f6f8fa; }
  .box { max-width: 720px; margin:0 auto; background:#fff; border:1px solid #d0d7de; border-radius:10px; padding:28px 32px; }
  h1 { font-size:18px; margin:0 0 4px; }
  .ok { color:#1a7f37; font-weight:600; }
  p { margin:12px 0; }
  code, pre { font-family: ui-monospace, Consolas, monospace; background:#f6f8fa; border:1px solid #d0d7de; border-radius:6px; }
  code { padding:1px 5px; font-size:13px; }
  pre { padding:12px 14px; overflow:auto; font-size:13px; }
  ol { padding-left:22px; }
  li { margin:6px 0; }
</style>
</head>
<body>
<div class="box">
  <h1>后端已在运行，但前端还没有内嵌进来</h1>
  <p class="ok">✓ 服务本身是正常的：<code>/api/v1/healthz</code> 可以访问。</p>
  <p>当前这个二进制里没有前端构建产物。两种做法：</p>
  <ol>
    <li>
      <strong>要单文件交付</strong>（推荐）—— 在仓库根目录执行构建脚本，它会
      构建前端、同步到内嵌目录、再编译后端：
      <pre>bash scripts/build.sh</pre>
    </li>
    <li>
      <strong>要改前端代码</strong> —— 用开发服务器，改动即时生效：
      <pre>cd web &amp;&amp; npm install &amp;&amp; npm run dev</pre>
      然后访问 <code>http://127.0.0.1:5173</code>（<code>/api</code> 会自动代理到本服务）。
    </li>
  </ol>
  <p style="color:#59636e">这个提示页不会进浏览器缓存；构建完刷新即可看到真实界面。</p>
</div>
</body>
</html>
`
