package share

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/internal/games"
)

// Page 是带 OG 标签的 /g/{code} 页面（§5.4）。
//
// 微信和 WhatsApp 的链接抓取器不执行 JS，所以每一局的预览卡片（标题、描述、图）必须由服务端写进 HTML。
// 地点、称呼、备注都是用户输入：**只能用 html/template 渲染**，它会按上下文转义（§10.6）。
// 构建出来的 index.html 本身当作不可信的模板以外的静态字节处理：只在字节层面切掉原来的 <title>，
// 再把模板渲染出的 <title> 和 meta 插进 </head> 之前，用户输入从不和 HTML 字符串直接拼接。
type Page struct {
	before []byte // </head> 之前的部分（已去掉原来的 <title>）
	after  []byte // </head> 及之后
	index  []byte // 原样的 index.html：牌局不存在时直接返回
}

// NewPage 读取前端构建产物里的 index.html。
func NewPage(dist fs.FS) (*Page, error) {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil, fmt.Errorf("读取 index.html: %w", err)
	}
	i := bytes.Index(index, []byte("</head>"))
	if i < 0 {
		return nil, errors.New("index.html 里没有 </head>")
	}
	head := index[:i]
	// 去掉原来的 <title>…</title>，换成这一局的。
	if s := bytes.Index(head, []byte("<title>")); s >= 0 {
		if e := bytes.Index(head[s:], []byte("</title>")); e >= 0 {
			head = append(append([]byte{}, head[:s]...), head[s+e+len("</title>"):]...)
		}
	}
	return &Page{before: head, after: index[i:], index: index}, nil
}

var ogTemplate = template.Must(template.New("og").Parse(`<title>{{.Title}}</title>
    <meta name="description" content="{{.Description}}" />
    <meta property="og:type" content="website" />
    <meta property="og:site_name" content="随时掼掼蛋" />
    <meta property="og:title" content="{{.Title}}" />
    <meta property="og:description" content="{{.Description}}" />
    <meta property="og:url" content="{{.URL}}" />
    <meta property="og:image" content="{{.Image}}" />
    <meta property="og:image:width" content="1200" />
    <meta property="og:image:height" content="630" />
    <meta name="twitter:card" content="summary_large_image" />
    <meta name="twitter:title" content="{{.Title}}" />
    <meta name="twitter:description" content="{{.Description}}" />
    <meta name="twitter:image" content="{{.Image}}" />
  `))

// OGData 是一局的预览卡片内容。
type OGData struct {
	Title       string
	Description string
	URL         string
	Image       string
}

// OGFor 生成一局的预览内容。now 用来判断「今天」「明天」。
func OGFor(d *games.Detail, publicURL string, now time.Time) OGData {
	g := d.Game
	base := strings.TrimSuffix(publicURL, "/") + "/g/" + g.ShortCode
	title := "掼蛋局 · " + dayLabel(g.StartsAt, now) + " " + g.StartsAt.In(Jakarta).Format("15:04") + " · " + g.Venue
	var status string
	switch {
	case g.Status == games.StatusCancelled:
		title = "【已取消】" + title
		status = "这局已取消"
	case g.Status == games.StatusDone:
		status = "这局已结束"
	case d.Missing <= 0:
		status = "已成局"
	default:
		status = fmt.Sprintf("还差 %d 人，点开占座", d.Missing)
	}
	desc := status + " · 局头 " + d.HostName
	if g.Note != "" {
		desc += " · " + g.Note
	}
	return OGData{
		Title:       title,
		Description: desc,
		URL:         base,
		// 带上版本号和雅加达当天日期：抓取器和聊天软件会按地址缓存图片，局有变化、
		// 或者跨过午夜（图上的「明天」该变成「今天」）时换一个地址。
		Image: fmt.Sprintf("%s/og.png?v=%d-%d-%s", base, g.Version, len(d.Seats), jakartaDay(now)),
	}
}

// Render 返回注入了 OG 标签的页面。
func (p *Page) Render(og OGData) ([]byte, error) {
	var b bytes.Buffer
	b.Grow(len(p.index) + 1024)
	b.Write(p.before)
	if err := ogTemplate.Execute(&b, og); err != nil {
		return nil, err
	}
	b.Write(p.after)
	return b.Bytes(), nil
}

// Index 是原样的 index.html。
func (p *Page) Index() []byte { return p.index }

// dayLabel 是「今天」「明天」「后天」，再往后是「10/16 周五」（雅加达时间）。
func dayLabel(t, now time.Time) string {
	lt, ln := t.In(Jakarta), now.In(Jakarta)
	dt := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, Jakarta)
	dn := time.Date(ln.Year(), ln.Month(), ln.Day(), 0, 0, 0, 0, Jakarta)
	weekday := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[lt.Weekday()]
	md := fmt.Sprintf("%d/%d %s", lt.Month(), lt.Day(), weekday)
	switch int(dt.Sub(dn).Hours() / 24) {
	case 0:
		return "今天 " + md
	case 1:
		return "明天 " + md
	case 2:
		return "后天 " + md
	default:
		return md
	}
}
