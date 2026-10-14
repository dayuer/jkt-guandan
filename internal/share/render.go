package share

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
	"rsc.io/qr"

	"github.com/dayuer/jkt-guandan/internal/games"
)

// OG 图和海报的服务端渲染（§5.4），视觉按设计稿 C2（海报成品），颜色取自 design/guandan-h5/tokens.css。
//
// 字体是 Noto Sans SC Bold 和 Noto Serif SC Black 的子集（ASCII、Latin-1、标点、GB2312 汉字，
// 见 scripts/fonts/subset.sh），嵌在二进制里，SIL OFL 1.1。每个字依次在衬线、无衬线里找字形，
// 都没有（比如 emoji、生僻字）就跳过，不会画成方框。

const (
	OGWidth      = 1200
	OGHeight     = 630
	PosterWidth  = 1080
	PosterHeight = 1440
)

var (
	//go:embed fonts/NotoSansSC-Bold.subset.otf
	sansData []byte
	//go:embed fonts/NotoSerifSC-Black.subset.otf
	serifData []byte

	fontsOnce sync.Once
	sansFont  *opentype.Font
	serifFont *opentype.Font
	fontsErr  error
)

func loadFonts() error {
	fontsOnce.Do(func() {
		if sansFont, fontsErr = opentype.Parse(sansData); fontsErr != nil {
			return
		}
		serifFont, fontsErr = opentype.Parse(serifData)
	})
	return fontsErr
}

// tokens.css 里的颜色。
var (
	colRed     = hex(0xb3352c)
	colRedDeep = hex(0x8e2a23)
	colRedInk  = hex(0x7a231d)
	colCream   = hex(0xfff7ec)
	colSand    = hex(0xf1e4cc)
	colGold    = hex(0xe9c46a)
	colInk     = hex(0x2a1c17)
)

func hex(v uint32) color.RGBA {
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}

func withAlpha(c color.RGBA, a float64) color.RGBA {
	// 预乘 alpha。
	return color.RGBA{uint8(float64(c.R) * a), uint8(float64(c.G) * a), uint8(float64(c.B) * a), uint8(255 * a)}
}

// Card 是渲染需要的牌局信息。
type Card struct {
	StartsAt time.Time
	Now      time.Time // 用来判断「今天」「明天」
	Venue    string
	Seats    []string // 座位上的人的称呼，按入座顺序，最多 4 个
	Missing  int
	Status   string
	URL      string // 二维码指向的牌局页面
}

// CardFor 把牌局转成渲染用的信息。
func CardFor(d *games.Detail, publicURL string, now time.Time) Card {
	c := Card{
		StartsAt: d.Game.StartsAt, Now: now, Venue: d.Game.Venue,
		Missing: d.Missing, Status: d.Game.Status,
		URL: strings.TrimSuffix(publicURL, "/") + "/g/" + d.Game.ShortCode,
	}
	for _, s := range d.Seats {
		c.Seats = append(c.Seats, s.Nickname)
	}
	return c
}

// ---- 画布 ----

type canvas struct {
	img   *image.RGBA
	faces map[faceKey]font.Face
}

type faceKey struct {
	f    *opentype.Font
	size float64
}

func newCanvas(w, h int, bg color.Color) *canvas {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	return &canvas{img: img, faces: map[faceKey]font.Face{}}
}

func (c *canvas) close() {
	for _, f := range c.faces {
		_ = f.Close()
	}
}

func (c *canvas) face(f *opentype.Font, size float64) font.Face {
	k := faceKey{f, size}
	if face, ok := c.faces[k]; ok {
		return face
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		panic(err) // 字体在 loadFonts 里已经验证过，这里不会失败
	}
	c.faces[k] = face
	return face
}

// fill 用光栅器把一条路径填上颜色（抗锯齿）。
func (c *canvas) fill(col color.Color, path func(r *vector.Rasterizer)) {
	b := c.img.Bounds()
	r := vector.NewRasterizer(b.Dx(), b.Dy())
	r.DrawOp = draw.Over
	path(r)
	r.Draw(c.img, b, image.NewUniform(col), image.Point{})
}

func roundRect(r *vector.Rasterizer, x0, y0, x1, y1, rad float64) {
	const k = 0.5523 // 用三次贝塞尔近似四分之一圆
	r.MoveTo(float32(x0+rad), float32(y0))
	r.LineTo(float32(x1-rad), float32(y0))
	r.CubeTo(float32(x1-rad+rad*k), float32(y0), float32(x1), float32(y0+rad-rad*k), float32(x1), float32(y0+rad))
	r.LineTo(float32(x1), float32(y1-rad))
	r.CubeTo(float32(x1), float32(y1-rad+rad*k), float32(x1-rad+rad*k), float32(y1), float32(x1-rad), float32(y1))
	r.LineTo(float32(x0+rad), float32(y1))
	r.CubeTo(float32(x0+rad-rad*k), float32(y1), float32(x0), float32(y1-rad+rad*k), float32(x0), float32(y1-rad))
	r.LineTo(float32(x0), float32(y0+rad))
	r.CubeTo(float32(x0), float32(y0+rad-rad*k), float32(x0+rad-rad*k), float32(y0), float32(x0+rad), float32(y0))
	r.ClosePath()
}

func circle(r *vector.Rasterizer, cx, cy, rad float64) {
	roundRect(r, cx-rad, cy-rad, cx+rad, cy+rad, rad)
}

// dashedRoundRect 沿圆角矩形的边画一圈小短线（设计稿里卡片内圈的虚线）。
func (c *canvas) dashedRoundRect(x0, y0, x1, y1, rad, dash, gap, width float64, col color.Color) {
	pts := roundRectPoints(x0, y0, x1, y1, rad, 1)
	c.dashAlong(pts, dash, gap, width, col)
}

// dashedCircle 画虚线圆（空座位）。
func (c *canvas) dashedCircle(cx, cy, rad, dash, gap, width float64, col color.Color) {
	n := int(2 * math.Pi * rad)
	pts := make([][2]float64, 0, n+1)
	for i := 0; i <= n; i++ {
		a := 2 * math.Pi * float64(i) / float64(n)
		pts = append(pts, [2]float64{cx + rad*math.Cos(a), cy + rad*math.Sin(a)})
	}
	c.dashAlong(pts, dash, gap, width, col)
}

// dashAlong 沿一串间隔约 1 像素的点画短线：每段画成一个细长的四边形。
func (c *canvas) dashAlong(pts [][2]float64, dash, gap, width float64, col color.Color) {
	c.fill(col, func(r *vector.Rasterizer) {
		period := dash + gap
		dist := 0.0
		for i := 1; i < len(pts); i++ {
			ax, ay, bx, by := pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1]
			seg := math.Hypot(bx-ax, by-ay)
			if seg == 0 {
				continue
			}
			if math.Mod(dist, period) < dash {
				nx, ny := -(by-ay)/seg*width/2, (bx-ax)/seg*width/2
				r.MoveTo(float32(ax+nx), float32(ay+ny))
				r.LineTo(float32(bx+nx), float32(by+ny))
				r.LineTo(float32(bx-nx), float32(by-ny))
				r.LineTo(float32(ax-nx), float32(ay-ny))
				r.ClosePath()
			}
			dist += seg
		}
	})
}

// roundRectPoints 把圆角矩形的边离散成间隔约 step 像素的点（顺时针）。
func roundRectPoints(x0, y0, x1, y1, rad, step float64) [][2]float64 {
	var pts [][2]float64
	line := func(ax, ay, bx, by float64) {
		n := int(math.Max(1, math.Hypot(bx-ax, by-ay)/step))
		for i := 0; i < n; i++ {
			t := float64(i) / float64(n)
			pts = append(pts, [2]float64{ax + (bx-ax)*t, ay + (by-ay)*t})
		}
	}
	arc := func(cx, cy, a0 float64) {
		n := int(math.Max(1, rad*math.Pi/2/step))
		for i := 0; i < n; i++ {
			a := a0 + math.Pi/2*float64(i)/float64(n)
			pts = append(pts, [2]float64{cx + rad*math.Cos(a), cy + rad*math.Sin(a)})
		}
	}
	line(x0+rad, y0, x1-rad, y0)
	arc(x1-rad, y0+rad, -math.Pi/2)
	line(x1, y0+rad, x1, y1-rad)
	arc(x1-rad, y1-rad, 0)
	line(x1-rad, y1, x0+rad, y1)
	arc(x0+rad, y1-rad, math.Pi/2)
	line(x0, y1-rad, x0, y0+rad)
	arc(x0+rad, y0+rad, math.Pi)
	pts = append(pts, pts[0])
	return pts
}

// ---- 文字：逐字回退 ----

type textStyle struct {
	fonts []*opentype.Font // 依次尝试
	size  float64
	color color.Color
}

// pick 给一个字找到第一个有字形的字体；都没有返回 nil（这个字跳过）。
func pick(r rune, fonts []*opentype.Font) *opentype.Font {
	var buf sfntBuffer
	for _, f := range fonts {
		if idx, err := f.GlyphIndex(&buf.b, r); err == nil && idx != 0 {
			return f
		}
	}
	return nil
}

// renderable 去掉没有字形的字（emoji 等）和控制字符。
func renderable(s string, fonts []*opentype.Font) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		if r == ' ' || pick(r, fonts) != nil {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (c *canvas) measure(s string, st textStyle) float64 {
	var w fixed.Int26_6
	for _, r := range s {
		f := pick(r, st.fonts)
		if f == nil {
			if r == ' ' {
				f = st.fonts[len(st.fonts)-1]
			} else {
				continue
			}
		}
		adv, ok := c.face(f, st.size).GlyphAdvance(r)
		if ok {
			w += adv
		}
	}
	return float64(w) / 64
}

// text 在基线 y 上画字。align：0 左对齐，0.5 居中，1 右对齐。
func (c *canvas) text(s string, x, y, align float64, st textStyle) {
	x -= c.measure(s, st) * align
	dot := fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}
	src := image.NewUniform(st.color)
	for _, r := range s {
		f := pick(r, st.fonts)
		if f == nil {
			if r != ' ' {
				continue
			}
			f = st.fonts[len(st.fonts)-1]
		}
		face := c.face(f, st.size)
		d := font.Drawer{Dst: c.img, Src: src, Face: face, Dot: dot}
		d.DrawString(string(r))
		dot = d.Dot
	}
}

// fit 截断过长的文字，末尾加「…」。
func (c *canvas) fit(s string, maxW float64, st textStyle) string {
	if c.measure(s, st) <= maxW {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 {
		rs = rs[:len(rs)-1]
		if t := string(rs) + "…"; c.measure(t, st) <= maxW {
			return t
		}
	}
	return "…"
}

// initial 是座位圆点里显示的字：称呼里第一个能画出来的字，拉丁字母转大写。
func initial(name string, fonts []*opentype.Font) string {
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || pick(r, fonts) == nil {
			continue
		}
		return string(unicode.ToUpper(r))
	}
	return "牌"
}

// ---- 二维码 ----

func (c *canvas) qr(url string, x, y, size float64, col color.Color) error {
	code, err := qr.Encode(url, qr.M)
	if err != nil {
		return err
	}
	n := float64(code.Size)
	m := size / n
	c.fill(col, func(r *vector.Rasterizer) {
		for j := 0; j < code.Size; j++ {
			for i := 0; i < code.Size; i++ {
				if code.Black(i, j) {
					x0, y0 := x+float64(i)*m, y+float64(j)*m
					// 模块之间多盖 0.5 像素，避免抗锯齿留下细缝。
					r.MoveTo(float32(x0), float32(y0))
					r.LineTo(float32(x0+m+0.5), float32(y0))
					r.LineTo(float32(x0+m+0.5), float32(y0+m+0.5))
					r.LineTo(float32(x0), float32(y0+m+0.5))
					r.ClosePath()
				}
			}
		}
	})
	return nil
}

// ---- 版式 ----

func statusPill(card Card) string {
	switch {
	case card.Status == games.StatusCancelled:
		return "已取消"
	case card.Status == games.StatusDone:
		return "已结束"
	case card.Missing <= 0:
		return "已成局"
	default:
		return fmt.Sprintf("差 %d 人", card.Missing)
	}
}

// seatsRow 画 4 个座位圆点：有人的是奶油色实心圆加称呼首字，空位是虚线圆。
func (c *canvas) seatsRow(card Card, cx, cy, rad, gap float64) {
	serifThenSans := []*opentype.Font{serifFont, sansFont}
	total := 4*2*rad + 3*gap
	x := cx - total/2 + rad
	for i := 0; i < 4; i++ {
		if i < len(card.Seats) {
			c.fill(colCream, func(r *vector.Rasterizer) { circle(r, x, cy, rad) })
			st := textStyle{serifThenSans, rad * 0.8, colRed}
			c.text(initial(card.Seats[i], serifThenSans), x, cy+rad*0.28, 0.5, st)
		} else {
			c.dashedCircle(x, cy, rad-2, 9, 7, 3.5, withAlpha(colCream, 0.75))
		}
		x += 2*rad + gap
	}
}

func (c *canvas) pill(text string, cx, cy, h float64, card Card) {
	st := textStyle{[]*opentype.Font{sansFont}, h * 0.44, colRed}
	bg := colCream
	if card.Status == games.StatusCancelled || card.Status == games.StatusDone {
		st.color, bg = colCream, colInk
	}
	w := c.measure(text, st) + h*1.1
	c.fill(bg, func(r *vector.Rasterizer) { roundRect(r, cx-w/2, cy-h/2, cx+w/2, cy+h/2, h*0.3) })
	c.text(text, cx, cy+st.size*0.36, 0.5, st)
}

// timeCard 画红色牌桌卡片：外圈深红、内圈虚线、日期、大号时间、地点。
func (c *canvas) timeCard(card Card, x0, y0, x1, y1, scale float64) {
	// 投影
	c.fill(withAlpha(colInk, 0.18), func(r *vector.Rasterizer) { roundRect(r, x0, y0+14*scale, x1, y1+18*scale, 60*scale) })
	c.fill(colRedInk, func(r *vector.Rasterizer) { roundRect(r, x0, y0, x1, y1, 60*scale) })
	in := 16 * scale
	c.fill(colRedDeep, func(r *vector.Rasterizer) { roundRect(r, x0+in, y0+in, x1-in, y1-in, 48*scale) })
	d := 42 * scale
	c.dashedRoundRect(x0+d, y0+d, x1-d, y1-d, 30*scale, 9*scale, 7*scale, 2.5*scale, withAlpha(colGold, 0.6))

	cx := (x0 + x1) / 2
	h := y1 - y0
	sans := []*opentype.Font{sansFont}
	serif := []*opentype.Font{serifFont, sansFont}
	date := dayLabel(card.StartsAt, card.Now)
	c.text(date, cx, y0+h*0.23, 0.5, textStyle{sans, 44 * scale, colSand})
	clock := card.StartsAt.In(Jakarta).Format("15:04")
	c.text(clock, cx, y0+h*0.63, 0.5, textStyle{serif, 210 * scale, colCream})
	vst := textStyle{sans, 44 * scale, colSand}
	venue := c.fit(renderable(card.Venue, sans), (x1-x0)-2*d-40*scale, vst)
	c.text(venue, cx, y0+h*0.85, 0.5, vst)
}

// RenderPoster 渲染 1080×1440 的海报（设计稿 C2）。
func RenderPoster(card Card) (image.Image, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	c := newCanvas(PosterWidth, PosterHeight, colRed)
	defer c.close()
	// 右上角淡淡的装饰圆（设计稿里的光斑）。
	c.fill(withAlpha(colCream, 0.06), func(r *vector.Rasterizer) { circle(r, 1060, 260, 230) })

	c.timeCard(card, 92, 95, 988, 830, 1)
	c.seatsRow(card, PosterWidth/2, 970, 63, 37)
	c.pill(statusPill(card), PosterWidth/2, 1133, 100, card)
	c.fill(withAlpha(colCream, 0.25), func(r *vector.Rasterizer) {
		r.MoveTo(92, 1188)
		r.LineTo(988, 1188)
		r.LineTo(988, 1190)
		r.LineTo(92, 1190)
		r.ClosePath()
	})
	serif := []*opentype.Font{serifFont, sansFont}
	c.text("随时掼掼蛋 · 雅加达", 92, 1318, 0, textStyle{serif, 52, colCream})
	c.text("扫码占座 · 纯娱乐，不设彩头", 92, 1378, 0, textStyle{[]*opentype.Font{sansFont}, 28, withAlpha(colCream, 0.8)})

	// 二维码：奶油色圆角底，指向牌局页面。
	c.fill(colCream, func(r *vector.Rasterizer) { roundRect(r, 783, 1218, 988, 1423, 22) })
	if err := c.qr(card.URL, 797, 1232, 177, colInk); err != nil {
		return nil, err
	}
	return c.img, nil
}

// RenderOG 渲染 1200×630 的链接预览图：左边牌桌卡片，右边座位、状态和品牌。
func RenderOG(card Card) (image.Image, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	c := newCanvas(OGWidth, OGHeight, colRed)
	defer c.close()
	c.fill(withAlpha(colCream, 0.06), func(r *vector.Rasterizer) { circle(r, 1180, 40, 200) })
	c.timeCard(card, 50, 50, 650, 570, 0.7)
	c.seatsRow(card, 925, 220, 52, 26)
	c.pill(statusPill(card), 925, 345, 84, card)
	serif := []*opentype.Font{serifFont, sansFont}
	c.text("随时掼掼蛋 · 雅加达", 925, 500, 0.5, textStyle{serif, 44, colCream})
	c.text("点开占座 · 纯娱乐，不设彩头", 925, 548, 0.5, textStyle{[]*opentype.Font{sansFont}, 24, withAlpha(colCream, 0.8)})
	return c.img, nil
}

// RenderGenericOG 是兜底的通用 OG 图（§12.5：渲染失败时返回它）。只用纯色和形状也能画出来，
// 字体可用时再加上品牌字。
func RenderGenericOG() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, OGWidth, OGHeight))
	draw.Draw(img, img.Bounds(), image.NewUniform(colRed), image.Point{}, draw.Src)
	c := &canvas{img: img, faces: map[faceKey]font.Face{}}
	defer c.close()
	c.fill(colRedInk, func(r *vector.Rasterizer) { roundRect(r, 300, 115, 900, 515, 50) })
	c.fill(colRedDeep, func(r *vector.Rasterizer) { roundRect(r, 312, 127, 888, 503, 40) })
	if loadFonts() == nil {
		serif := []*opentype.Font{serifFont, sansFont}
		c.text("随时掼掼蛋", 600, 340, 0.5, textStyle{serif, 96, colCream})
		c.text("雅加达 · 约掼蛋", 600, 420, 0.5, textStyle{[]*opentype.Font{sansFont}, 36, colSand})
	}
	return img
}

// EncodePNG 把图编码成 PNG。
func EncodePNG(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// sfntBuffer 包一层 sfnt.Buffer，GlyphIndex 要用；每次调用新建，并发安全。
type sfntBuffer struct{ b sfnt.Buffer }
