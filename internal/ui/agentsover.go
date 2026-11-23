package ui

import (
	"slices"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/term"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
)

// This file opens the agent view (agentview.go) over a session, as Claude
// Code's ← on an empty prompt opens its agent view from a session: the
// first ← says a second opens it, the second does. The session goes on
// underneath, as it does under the transcript view: what it says
// meanwhile is held, and goes into the scrollback when the view closes.
// Esc (or /exit) comes back to it. Enter on a run that is over opens that
// run's session in place of this one (/resume), which is kept; a question
// of the session's own closes the view, so that it is seen.

// agentViewer is what a session's controls offer when the agent view can
// be opened over the session: the runs it lists, and how it is set up.
type agentViewer interface {
	AgentView() (app.AgentViewInfo, error)
}

// agentsOverlay is the agent view while it is open over the session.
type agentsOverlay struct {
	v   *agentView
	scr agentScreen
	// held is what the session drew meanwhile, and panel says the side
	// panel was on, and comes back after.
	held  heldScreen
	panel bool
}

// agentsTwice is how soon a second ← must follow the first to open the
// agent view.
const agentsTwice = 2 * time.Second

// agentsLook is how often the session's loop gives the open view the
// time, for its clocks and to look at the runs again, and agentsHintFor
// how long a session's status line says "← for agents" as it begins.
const (
	agentsLook    = 250 * time.Millisecond
	agentsHintFor = 10 * time.Second
)

// agentsOffered reports whether this session can open the agent view.
func (s *session) agentsOffered() bool {
	_, ok := s.c.(agentViewer)
	return ok
}

// leftKey is ← in the empty prompt box: the first press says that a
// second opens the agent view, and a second soon after opens it.
func (s *session) leftKey() {
	now := s.now()
	if !s.leftAt.IsZero() && now.Sub(s.leftAt) < agentsTwice {
		s.leftAt = time.Time{}
		s.openAgents()
		return
	}
	s.leftAt = now
	s.flash("Press ← again for the agent view: the runs in the background, of every folder.")
}

// openAgents takes the screen for the agent view.
func (s *session) openAgents() {
	av, ok := s.c.(agentViewer)
	if !ok {
		return
	}
	info, err := av.AgentView()
	if err != nil {
		s.notice(agent.LevelWarning, "The agent view could not be opened: "+err.Error())
		return
	}
	cols, rows, ok := s.host.size()
	if !ok {
		cols, rows = s.termCols, s.rows
	}
	// (What waits to be written goes into the scrollback first.)
	s.frame()
	panel := s.panel != nil
	s.closePanel()
	p, err := s.host.openView(cols, rows)
	if err != nil {
		s.notice(agent.LevelWarning, "The agent view could not be opened: "+err.Error())
		if panel {
			s.openPanel()
		}
		return
	}
	v := newAgentView(s.th, s.m, s.now, info)
	v.async = func(work func() func(*agentView)) {
		s.host.spawn(func() func() {
			then := work()
			return func() {
				// (A view closed meanwhile takes it too, and is not
				// drawn again.)
				then(v)
				s.dirty = true
			}
		})
	}
	v.refresh()
	s.agents = &agentsOverlay{v: v, scr: agentScreen{p: p}, panel: panel}
	s.r = &s.agents.held
	s.termCols, s.rows = cols, rows
	s.dirty = true
}

// closeAgents gives the screen back, and writes what the session said
// meanwhile into the scrollback, where it would have been.
func (s *session) closeAgents() {
	o := s.agents
	if o == nil {
		return
	}
	_, err := o.scr.p.Close()
	s.keep(err)
	s.agents, s.r = nil, s.inline
	if cols, rows, ok := s.host.size(); ok {
		s.termCols, s.rows = cols, rows
	}
	s.keep(s.inline.Resize(s.termCols, s.rows))
	if len(o.held.lines) > 0 {
		s.keep(s.inline.Commit(o.held.lines...))
	}
	s.relayout(s.termCols)
	s.dirty = true
	if o.panel {
		s.openPanel()
	}
}

// agentsInput gives what the terminal reported to the open view, and does
// what the view then asks for: to leave, to open a run that is over (in
// place of this session), to stop the process (Ctrl+Z), or to draw the
// screen afresh.
func (s *session) agentsInput(ev term.Event) {
	s.dirty = true
	o := s.agents
	v := o.v
	v.input(ev)
	switch {
	case v.quit:
		s.closeAgents()
	case v.open != "":
		id := v.open
		s.closeAgents()
		s.command(editor.Draft{Text: "/resume " + id})
	case v.suspend:
		v.suspend = false
		s.closeAgents()
		s.suspendNow()
	case v.clear:
		v.clear = false
		o.scr.drawn = nil
		s.keep(o.scr.p.Clear())
	}
}

// agentsFrame draws the open view, and sends its notes (a run that needs
// input, one that is done or failed) by the person's channel.
func (s *session) agentsFrame() {
	o := s.agents
	s.keep(o.scr.draw(o.v))
	for _, note := range o.v.notes {
		s.host.desktop(s.info.NotifChannel, note)
	}
	o.v.notes = o.v.notes[:0]
}

// agentScreen draws an agent view on a panel of the whole screen: the
// list, or a run's conversation watched, which is laid into the screen
// again only when it changed, and scrolled as the view asks.
type agentScreen struct {
	p *term.Panel
	// drawn is what the screen's rows from the top show, and watched the
	// run whose conversation they are, or "".
	drawn   []string
	watched string
}

func (a *agentScreen) draw(v *agentView) error {
	p := a.p
	cols, rows := p.Size()
	body, live, crow, ccol := v.render(cols, rows)
	w := v.watch
	switch {
	case w == nil && a.watched != "":
		a.watched, a.drawn = "", nil
		p.Scroll(-1 << 30)
	case w != nil && a.watched != w.id:
		a.watched, w.end, a.drawn = w.id, true, nil
	}
	var err error
	if a.drawn != nil && slices.Equal(body, a.drawn) {
		err = p.SetLive(live, crow, ccol)
	} else {
		a.drawn = body
		err = p.SetView(body, live, crow, ccol)
	}
	if w == nil {
		return err
	}
	if w.start {
		p.ScrollTo(0)
	}
	if n := w.rows + w.pages*p.Page(); n != 0 {
		p.Scroll(n)
	}
	if w.end {
		p.Scroll(-1 << 30)
	}
	w.rows, w.pages, w.start, w.end = 0, 0, false, false
	return err
}
