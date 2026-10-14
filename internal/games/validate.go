package games

import (
	"fmt"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/internal/moderate"
	"github.com/dayuer/jkt-guandan/internal/textclean"
)

// 输入校验的长度和范围（§12.2）。
const (
	VenueMax = 40
	NoteMax  = 200
	// 开局时间必须在现在之后 10 分钟到 30 天之间。
	MinLeadTime = 10 * time.Minute
	MaxLeadTime = 30 * 24 * time.Hour
)

// CreateInput 是开一局的输入。
type CreateInput struct {
	StartsAt time.Time
	Area     string
	Venue    string
	Kind     string
	Note     string
}

// Clean 去掉控制字符、零宽/BiDi 格式字符和首尾空白。
func Clean(s string) string {
	return textclean.Clean(s)
}

// Normalize 校验并整理开局的输入。now 由调用方从 clock 取，方便测试。
func (in *CreateInput) Normalize(now time.Time) error {
	in.Venue = Clean(in.Venue)
	in.Note = Clean(in.Note)
	in.Area = Clean(in.Area)
	in.Kind = Clean(in.Kind)

	if in.Kind == "" {
		in.Kind = "classic"
	}
	if !contains(Kinds, in.Kind) {
		return fmt.Errorf("%w：kind 只能是 %s", ErrInvalidInput, strings.Join(Kinds, " / "))
	}
	if !contains(Areas, in.Area) {
		return fmt.Errorf("%w：片区不在允许的列表里", ErrInvalidInput)
	}

	// 地点按字符数算，一个汉字算一个。
	if n := len([]rune(in.Venue)); n < 1 || n > VenueMax {
		return fmt.Errorf("%w：地点需要 1–%d 个字符", ErrInvalidInput, VenueMax)
	}
	if n := len([]rune(in.Note)); n > NoteMax {
		return fmt.Errorf("%w：备注不能超过 %d 个字符", ErrInvalidInput, NoteMax)
	}

	// 备注和地点不能涉及金钱或赌博（§14）。放在长度检查之后：先说「太长了」
	// 比先说「别写钱」更贴切。
	if err := checkContent(in.Venue, in.Note); err != nil {
		return err
	}

	if in.StartsAt.IsZero() {
		return fmt.Errorf("%w：缺少开局时间", ErrInvalidInput)
	}
	lead := in.StartsAt.Sub(now)
	if lead < MinLeadTime || lead > MaxLeadTime {
		return fmt.Errorf("%w：开局时间要在 %d 分钟后到 %d 天之间",
			ErrInvalidInput, int(MinLeadTime.Minutes()), int(MaxLeadTime.Hours()/24))
	}
	return nil
}

// Patch 是局头修改牌局的输入。nil 表示这一项不动。
type Patch struct {
	StartsAt *time.Time
	Venue    *string
	Area     *string
	Kind     *string
	Note     *string
}

// Normalize 校验并整理修改的输入。
func (p *Patch) Normalize(now time.Time) error {
	if p.Venue != nil {
		v := Clean(*p.Venue)
		if n := len([]rune(v)); n < 1 || n > VenueMax {
			return fmt.Errorf("%w：地点需要 1–%d 个字符", ErrInvalidInput, VenueMax)
		}
		p.Venue = &v
	}
	if p.Note != nil {
		n := Clean(*p.Note)
		if len([]rune(n)) > NoteMax {
			return fmt.Errorf("%w：备注不能超过 %d 个字符", ErrInvalidInput, NoteMax)
		}
		p.Note = &n
	}
	if p.Area != nil {
		a := Clean(*p.Area)
		if !contains(Areas, a) {
			return fmt.Errorf("%w：片区不在允许的列表里", ErrInvalidInput)
		}
		p.Area = &a
	}
	if p.Kind != nil {
		k := Clean(*p.Kind)
		if !contains(Kinds, k) {
			return fmt.Errorf("%w：kind 只能是 %s", ErrInvalidInput, strings.Join(Kinds, " / "))
		}
		p.Kind = &k
	}
	if p.StartsAt != nil {
		lead := p.StartsAt.Sub(now)
		if lead < MinLeadTime || lead > MaxLeadTime {
			return fmt.Errorf("%w：开局时间要在 %d 分钟后到 %d 天之间",
				ErrInvalidInput, int(MinLeadTime.Minutes()), int(MaxLeadTime.Hours()/24))
		}
	}
	if p.StartsAt == nil && p.Venue == nil && p.Area == nil && p.Kind == nil && p.Note == nil {
		return fmt.Errorf("%w：没有要修改的内容", ErrInvalidInput)
	}
	// 只检查这次要改的字段：旧值里万一有词表后来才加的词，不该卡住局头改时间。
	if p.Venue != nil {
		if hit := moderate.Check("venue", *p.Venue); hit != nil {
			return hit
		}
	}
	if p.Note != nil {
		if hit := moderate.Check("note", *p.Note); hit != nil {
			return hit
		}
	}
	return nil
}

// checkContent 检查要写进牌局的自由文本有没有敏感词（§14）。
//
// 开局和改牌局都要过这一关：不能因为「改」绕过「开」时的过滤。
func checkContent(venue, note string) error {
	if hit := moderate.Check("venue", venue); hit != nil {
		return hit
	}
	if hit := moderate.Check("note", note); hit != nil {
		return hit
	}
	return nil
}

// TimeChanged 说明这次修改把开局时间改成了和库里不一样的值——动时间要重排提醒、推送改期（§5.1）。
// 原样重发同一个时间不算：不重排，也不给座位上的人发「改期」推送。
func (p Patch) TimeChanged(current time.Time) bool {
	return p.StartsAt != nil && !p.StartsAt.Equal(current)
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
