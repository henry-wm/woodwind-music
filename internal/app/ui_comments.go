package app

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/lyrics"
	"github.com/HengWh/woodwind-music/internal/music"
)

const (
	quietLeast = 8 * time.Second         // shortest quiet stretch that shows a top comment
	quietLead  = 1200 * time.Millisecond // delay into the quiet stretch before showing
	quietTail  = 1000 * time.Millisecond // hide before the next vocal line arrives
	quietSlot  = 10 * time.Second        // approximate duration each comment shows
	quietRunes = 140                     // longest comment shown in a quiet stretch
)

// npCommentState holds comments for the track currently shown in Now Playing.
type npCommentState struct {
	track   string
	src     music.Source
	id      string
	list    []music.Comment
	total   int
	more    bool
	loaded  bool
	loading bool
	err     string
	draft   string
	posting bool
}

// quietState caches the quiet intervals for the current track's lyrics.
type quietState struct {
	track  string
	lines  int
	quiets []lyrics.Quiet
}

func (a *app) trackSource(tr *music.Track) music.Source {
	if tr == nil {
		return nil
	}
	if tr.Source == "" {
		return a.source()
	}
	return a.sourceByID(tr.Source)
}

func (a *app) trackCan(tr *music.Track, cap string) bool {
	src := a.trackSource(tr)
	return src != nil && src.Can(cap)
}

// commentsFor aligns Now Playing's comment state with tr and starts loading
// page 0 once when the track changes, so both the tab counter and quiet-part
// top comments have data ready.
func (a *app) commentsFor(tr *music.Track) *npCommentState {
	cs := &a.npComments
	if tr == nil {
		return cs
	}
	key := tr.Source + "\x00" + tr.ID
	if cs.track != key {
		*cs = npCommentState{track: key, src: a.trackSource(tr), id: tr.ID}
		if cs.src != nil && cs.src.Can("comments") {
			a.loadNPComments(false)
		}
	}
	return cs
}

func (a *app) bumpComments() {
	if a.commentRev != nil && a.cx != nil && !a.cx.Disposed() {
		a.commentRev.Update(func(n int) int { return n + 1 })
	}
}

func (a *app) loadNPComments(more bool) {
	cs := &a.npComments
	if cs.loading || cs.src == nil || !cs.src.Can("comments") {
		return
	}
	if !more && cs.loaded && cs.err == "" {
		return
	}
	src, track, id := cs.src, cs.track, cs.id
	offset := 0
	if more {
		offset = len(cs.list)
	}
	cs.loading, cs.err = true, ""
	go func() {
		page, err := src.Comments(id, offset)
		if err == nil && page == nil {
			err = errNoAnswer
		}
		a.post(func() {
			if a.cx.Disposed() || a.npComments.track != track {
				return
			}
			cs := &a.npComments
			cs.loading = false
			if err != nil {
				cs.err = err.Error()
				a.bumpComments()
				return
			}
			cs.loaded = true
			if !more {
				cs.list = nil
			}
			seen := make(map[string]bool, len(cs.list))
			for _, cm := range cs.list {
				seen[cm.ID] = true
			}
			for _, cm := range page.Comments {
				if !seen[cm.ID] {
					seen[cm.ID] = true
					cs.list = append(cs.list, cm)
				}
			}
			cs.total = page.Total
			if cs.total < len(cs.list) {
				cs.total = len(cs.list)
			}
			cs.more = page.More && len(page.Comments) > 0
			a.bumpComments()
		})
	}()
}

func (a *app) postNPComment(draft *core.Signal[string]) {
	cs := &a.npComments
	body := strings.TrimSpace(cs.draft)
	if body == "" || cs.posting || cs.src == nil || !cs.src.Can("postComment") {
		return
	}
	src, track, id := cs.src, cs.track, cs.id
	cs.posting = true
	a.bumpComments()
	go func() {
		cm, err := src.PostComment(id, body)
		a.post(func() {
			if a.cx.Disposed() || a.npComments.track != track {
				return
			}
			cs := &a.npComments
			cs.posting = false
			if err != nil {
				a.say(err.Error())
				a.bumpComments()
				return
			}
			cs.draft = ""
			if draft != nil {
				draft.Set("")
			}
			if cm != nil {
				cs.list = append([]music.Comment{*cm}, cs.list...)
				cs.total++
			}
			a.bumpComments()
		})
	}()
}

func (a *app) toggleNPCommentLike(commentID string) {
	cs := &a.npComments
	if cs.src == nil || !cs.src.Can("likeComment") {
		return
	}
	src, track, trackID := cs.src, cs.track, cs.id
	like := true
	if cm := cs.find(commentID); cm != nil {
		like = !cm.Liked
	}
	cs.setLiked(commentID, like)
	a.bumpComments()
	go func() {
		err := src.LikeComment(trackID, commentID, like)
		a.post(func() {
			if a.cx.Disposed() || a.npComments.track != track {
				return
			}
			if err != nil {
				a.npComments.setLiked(commentID, !like)
				a.say(err.Error())
				a.bumpComments()
			}
		})
	}()
}

func (cs *npCommentState) find(id string) *music.Comment {
	for i := range cs.list {
		if cs.list[i].ID == id {
			return &cs.list[i]
		}
	}
	return nil
}

func (cs *npCommentState) setLiked(id string, like bool) {
	if cm := cs.find(id); cm != nil && cm.Liked != like {
		cm.Liked = like
		if like {
			cm.Likes++
		} else {
			cm.Likes = max(cm.Likes-1, 0)
		}
	}
}

func (cs *npCommentState) topComments() []*music.Comment {
	var top []*music.Comment
	for i := range cs.list {
		cm := &cs.list[i]
		if cm.Likes > 0 {
			if n := utf8.RuneCountInString(cm.Text); n > 0 && n <= quietRunes {
				top = append(top, cm)
			}
		}
	}
	slices.SortStableFunc(top, func(x, y *music.Comment) int { return y.Likes - x.Likes })
	return top[:min(len(top), 10)]
}

// quietComment returns the top comment to show at pos during an intro,
// interlude, or outro, or nil when lyrics should remain visible.
func (a *app) quietComment(tr *music.Track, lines []lyrics.Line, loaded bool, pos time.Duration) *music.Comment {
	if !a.set.HotComments || tr == nil || tr.ShowID != "" {
		return nil
	}
	qs := &a.quiet
	key := tr.Source + "\x00" + tr.ID
	if qs.track != key || qs.lines != len(lines) || qs.quiets == nil {
		qs.track, qs.lines = key, len(lines)
		qs.quiets = lyrics.Quiets(lines, tr.Length(), quietLeast)
		if len(lines) == 0 && loaded && tr.Length() >= quietLeast {
			qs.quiets = []lyrics.Quiet{{From: 0, To: tr.Length()}}
		}
		if qs.quiets == nil {
			qs.quiets = []lyrics.Quiet{}
		}
	}
	if a.npComments.track != key {
		return nil
	}
	hot := a.npComments.topComments()
	if len(hot) == 0 {
		return nil
	}
	shown := 0
	for _, q := range qs.quiets {
		from, to := q.From+quietLead, q.To-quietTail
		if pos < from {
			return nil
		}
		if to <= from {
			continue
		}
		slots := max(1, int((to-from+quietSlot/2)/quietSlot))
		each := (to - from) / time.Duration(slots)
		if pos < to {
			slot := min(int((pos-from)/each), slots-1)
			return hot[(shown+slot)%len(hot)]
		}
		shown += slots
	}
	return nil
}

// hotCommentCard renders a top comment in place of lyrics during a quiet part.
func (a *app) hotCommentCard(cm *music.Comment, fontSize, maxWidth core.Points, onOpen func()) core.Node {
	if cm == nil {
		return core.Group{}
	}
	avatarPal := palette{sidebar: core.RGBA(0xffffff, 0.1), faint: core.RGBA(0xffffff, 0.4)}
	var meta []core.Node
	if cm.AvatarURL != "" {
		meta = append(meta, a.coverRound(avatarPal, cm.AvatarURL, 22))
	}
	meta = append(meta, core.Text{Content: cm.Author, MaxLines: 1,
		Style: core.Style{FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 13, Color: core.RGBA(0xffffff, 0.65)}})
	if cm.Likes > 0 {
		meta = append(meta, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Margin: core.Edges{Left: 6}},
			Children: []core.Node{
				icon(lucideThumbsUp, 13, core.RGBA(0xffffff, 0.45)),
				core.Text{Content: i18n.Count(cm.Likes), Style: core.Style{FontSize: 12, FontVariant: core.FontTabularNums, Color: core.RGBA(0xffffff, 0.45)}},
			}})
	}
	style := core.Style{Direction: core.Column, AlignItems: core.AlignStart, Gap: 14, Padding: core.Symmetric(16, 16),
		CornerRadius: 14, Hover: &core.Style{Background: core.RGBA(0xffffff, 0.06)}}
	if maxWidth > 0 {
		style.MaxWidth = core.Pt(maxWidth)
		style.Width = core.Pct(100)
	}
	return base.Button{TestID: "np-hot-comment", Label: i18n.T("View Comments"), OnPress: func(core.PressEvent) {
		if onOpen != nil {
			onOpen()
		}
	}, Style: style, Children: []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
			icon(lucideMessageCircle, 14, core.RGBA(0xffffff, 0.5)),
			core.Text{Content: i18n.T("Top Comment"), Style: core.Style{FontSize: 12.5, FontWeight: core.FontWeightBold, LetterSpacing: 0.6, Color: core.RGBA(0xffffff, 0.5)}},
		}},
		core.Text{Content: music.FormatCommentText(cm.Text), MaxLines: 6, Style: core.Style{FontSize: fontSize, FontWeight: core.FontWeightSemibold, LineHeightMultiple: 1.5, Color: core.RGBA(0xffffff, 0.92)}},
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: meta},
	}}
}

// commentsPane lists the comments of tr inside Now Playing, with posting and
// liking when supported by the track's source.
func (a *app) commentsPane(cx *core.Scope, p palette, tr *music.Track, white core.Color) core.Node {
	a.commentsFor(tr)
	cs := &a.npComments
	var top []core.Node
	if cs.src != nil && cs.src.Can("postComment") {
		draft := core.NewSignal(cx, cs.draft)
		disabled := func() bool {
			a.commentRev.Get()
			return strings.TrimSpace(draft.Get()) == "" || a.npComments.posting
		}
		top = append(top, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Margin: core.Edges{Bottom: 12}}, Children: []core.Node{
			core.Input{TestID: "np-comment-input", Label: i18n.T("Write a comment…"), Placeholder: i18n.T("Write a comment…"),
				Value:          draft.Get,
				OnChange:       func(v string) { a.npComments.draft = v; draft.Set(v) },
				OnSubmit:       func(string) { a.postNPComment(draft) },
				SubmitBehavior: core.SubmitBehaviorSubmit,
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Height: core.Pt(36), Padding: core.Symmetric(0, 12),
					CornerRadius: 10, Background: core.RGBA(0xffffff, 0.1), Color: white, PlaceholderColor: core.RGBA(0xffffff, 0.45), FontSize: 13.5}},
			base.Button{TestID: "np-comment-send", Label: i18n.T("Post"), Disabled: disabled,
				OnPress: func(core.PressEvent) { a.postNPComment(draft) },
				StyleFunc: func() core.Style {
					s := core.Style{Height: core.Pt(36), Padding: core.Symmetric(0, 16), CornerRadius: 999, Background: core.RGBA(0xffffff, 0.14),
						AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Hover: &core.Style{Background: core.RGBA(0xffffff, 0.2)}}
					if disabled() {
						s.Opacity = core.Alpha(0.45)
					}
					return s
				}, Children: []core.Node{text(i18n.T("Post"), 13, core.FontWeightSemibold, white)}},
		}})
	}
	list := core.Dynamic(func(cx *core.Scope) core.Node {
		a.commentRev.Get()
		cs := &a.npComments
		var body core.Node
		switch {
		case len(cs.list) == 0 && (cs.loading || (!cs.loaded && cs.err == "")):
			body = core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
				Children: []core.Node{text(i18n.T("Loading…"), 15, core.FontWeightNormal, core.RGBA(0xffffff, 0.45))}}
		case len(cs.list) == 0 && cs.err != "":
			body = core.View{Style: core.Style{FlexGrow: 1, Direction: core.Column, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 12},
				Children: []core.Node{
					text(cs.err, 14, core.FontWeightNormal, core.RGBA(0xffffff, 0.55)),
					base.Button{TestID: "np-comments-retry", Label: i18n.T("Retry"), OnPress: func(core.PressEvent) { a.loadNPComments(false); a.bumpComments() },
						Style:    core.Style{Height: core.Pt(34), Padding: core.Symmetric(0, 16), CornerRadius: 999, Background: core.RGBA(0xffffff, 0.14), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Hover: &core.Style{Background: core.RGBA(0xffffff, 0.2)}},
						Children: []core.Node{text(i18n.T("Retry"), 13, core.FontWeightSemibold, white)}},
				}}
		case len(cs.list) == 0:
			body = core.View{TestID: "np-comments-empty", Style: core.Style{FlexGrow: 1, Direction: core.Column, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 12},
				Children: []core.Node{
					icon(lucideMessageCircle, 36, core.RGBA(0xffffff, 0.3)),
					text(i18n.T("No comments yet"), 16, core.FontWeightSemibold, core.RGBA(0xffffff, 0.45)),
				}}
		default:
			now := a.localTime()
			canLike := cs.src != nil && cs.src.Can("likeComment")
			avatarPal := palette{sidebar: core.RGBA(0xffffff, 0.1), faint: core.RGBA(0xffffff, 0.4)}
			rows := make([]core.Node, 0, len(cs.list)+1)
			for i := range cs.list {
				cm := cs.list[i]
				var likeNode core.Node = core.Group{}
				if canLike || cm.Likes > 0 {
					fg := core.RGBA(0xffffff, 0.45)
					label := i18n.T("Like")
					if cm.Liked {
						fg = p.accent
						label = i18n.T("Unlike")
					}
					var children []core.Node
					children = append(children, icon(lucideThumbsUp, 13, fg))
					if cm.Likes > 0 {
						children = append(children, core.Text{Content: i18n.Count(cm.Likes), Style: core.Style{FontSize: 12, FontVariant: core.FontTabularNums, Color: fg}})
					}
					commentID := cm.ID
					likeNode = base.Button{TestID: "np-comment-like-" + cm.ID, Label: label, Disabled: func() bool { return !canLike },
						OnPress: func(core.PressEvent) { a.toggleNPCommentLike(commentID) },
						Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Padding: core.Symmetric(3, 8), CornerRadius: 999,
							Hover: &core.Style{Background: core.RGBA(0xffffff, 0.08)}},
						Children: children}
				}
				var col []core.Node
				col = append(col,
					core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
						core.Text{Content: cm.Author, MaxLines: 1, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 13, FontWeight: core.FontWeightSemibold, Color: core.RGBA(0xffffff, 0.65)}},
						likeNode,
					}},
					core.Text{Content: music.FormatCommentText(cm.Text), Style: core.Style{FontSize: 14.5, LineHeightMultiple: 1.55, Color: core.RGBA(0xffffff, 0.92)}},
				)
				if cm.Time > 0 {
					col = append(col, text(i18n.Ago(time.UnixMilli(cm.Time), now), 11.5, core.FontWeightNormal, core.RGBA(0xffffff, 0.38)))
				}
				rows = append(rows, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 12, Padding: core.Symmetric(12, 10), CornerRadius: 12}, Children: []core.Node{
					a.coverRound(avatarPal, cm.AvatarURL, 34),
					core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Direction: core.Column, Gap: 5}, Children: col},
				}})
			}
			var foot core.Node
			switch {
			case cs.loading:
				foot = text(i18n.T("Loading…"), 12.5, core.FontWeightNormal, core.RGBA(0xffffff, 0.45))
			case cs.err != "":
				foot = base.Button{TestID: "np-comments-more-retry", Label: i18n.T("Retry"), OnPress: func(core.PressEvent) { a.loadNPComments(true); a.bumpComments() },
					Style:    core.Style{Padding: core.Symmetric(6, 14), CornerRadius: 999, Background: core.RGBA(0xffffff, 0.1), Hover: &core.Style{Background: core.RGBA(0xffffff, 0.16)}},
					Children: []core.Node{text(i18n.T("Retry"), 12.5, core.FontWeightMedium, core.RGBA(0xffffff, 0.75))}}
			case cs.more:
				foot = base.Button{TestID: "np-comments-load-more", Label: i18n.T("Load More"), OnPress: func(core.PressEvent) { a.loadNPComments(true); a.bumpComments() },
					Style:    core.Style{Padding: core.Symmetric(6, 14), CornerRadius: 999, Background: core.RGBA(0xffffff, 0.1), Hover: &core.Style{Background: core.RGBA(0xffffff, 0.16)}},
					Children: []core.Node{text(i18n.T("Load More"), 12.5, core.FontWeightMedium, core.RGBA(0xffffff, 0.75))}}
			default:
				foot = text(i18n.T("That's all the comments"), 12, core.FontWeightNormal, core.RGBA(0xffffff, 0.35))
			}
			rows = append(rows, core.View{Style: core.Style{Height: core.Pt(48), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}, Children: []core.Node{foot}})
			body = core.Scroll{TestID: "np-comments-list", Style: core.Style{FlexGrow: 1, FlexBasis: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Column, Gap: 2, Padding: core.Edges{Right: 8, Bottom: 32}}, Children: rows},
			}}
		}
		return body
	})
	children := append(top, list)
	return core.View{TestID: "np-comments", Style: core.Style{FlexGrow: 1, FlexBasis: core.Pt(0), MinHeight: core.Pt(0), Direction: core.Column}, Children: children}
}

// showPane displays a podcast episode's description in Now Playing.
func (a *app) showPane(tr *music.Track, white core.Color) core.Node {
	desc := strings.TrimSpace(tr.Description)
	if desc == "" {
		desc = i18n.T("This episode has no description")
	}
	return core.Scroll{TestID: "np-show-about", Style: core.Style{FlexGrow: 1, FlexBasis: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{
		core.View{Style: core.Style{Padding: core.Edges{Top: 4, Right: 12, Bottom: 48}}, Children: []core.Node{
			core.Text{Content: desc, Style: core.Style{FontSize: 15, LineHeightMultiple: 1.7, Color: core.RGBA(0xffffff, 0.78)}},
		}},
	}}
}
