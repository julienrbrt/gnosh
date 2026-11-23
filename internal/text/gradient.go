package text

import (
	"unicode"
	"unicode/utf8"
)

// GradientOptions are the choices Gradient leaves at their defaults.
type GradientOptions struct {
	// Style gives the text its other attributes (bold, a background) and
	// the colour profile to write for. Its foreground colour is not used:
	// the gradient takes its place.
	Style Style
	// SkipSpaces leaves blank characters without any colour or style, so
	// that an underline or a background does not run through the gaps.
	// They still take their place along the gradient.
	SkipSpaces bool
}

// Gradient colours s along a gradient: the first grapheme cluster in the
// first of the stops, the last in the last, and those between in the colours
// between, in 24-bit colour. See GradientOptions.Render for the details and
// for other profiles.
func Gradient(s string, stops ...Color) string {
	return GradientOptions{}.Render(s, stops...)
}

// Render colours s along the gradient that runs through stops.
//
// Each grapheme cluster gets one foreground colour, chosen by the column it
// stands in: the gradient runs from the left edge to the right edge of the
// widest line of s, so in text of several lines (a wordmark drawn in block
// characters, say) the same column has the same colour on every line. The
// stops are spread evenly over that width. Each line is closed with a reset
// before its newline.
//
// The colours between two stops lie on the straight line between them in
// Oklab, a colour space made so that even steps look even: a blend of purple
// and pink passes through clean magentas at steady brightness, where a blend
// of the RGB numbers would dip darker and greyer in the middle. ANSI and
// palette stops stand for their colours in the xterm palette; Default stops
// are left out. With no stops left, s is rendered in the plain style.
//
// Under ANSI256 and ANSI16 each cluster is written in the nearest colour the
// profile has, which turns the gradient into a few bands; neighbours that
// share a colour share one escape sequence. Under NoColor s comes back
// unchanged. Escape sequences already in s are kept and take no room.
//
// Render takes time proportional to len(s), times the size of the palette
// under ANSI256 and ANSI16.
func (o GradientOptions) Render(s string, stops ...Color) string {
	profile := o.Style.profile()
	if profile == NoColor || s == "" {
		return s
	}
	ramp := make([]lab, 0, len(stops))
	for _, stop := range stops {
		if r, g, b, ok := stop.valid().rgb(); ok {
			ramp = append(ramp, toLab(r, g, b))
		}
	}
	if len(ramp) == 0 {
		return o.Style.Fg(Default).Render(s)
	}

	// The gradient spans the widest line.
	var m Metrics
	span, col := 0, 0
	for rest := s; rest != ""; {
		var p piece
		p, rest = m.nextPiece(rest)
		switch {
		case p.kind != Plain:
		case isNewline(p.text):
			span, col = max(span, col), 0
		default:
			col += p.width
		}
	}
	span = max(span, col)

	out := make([]byte, 0, len(s)+len(s)/2+32)
	on := false      // whether anything may be in effect on the line so far
	stale := true    // whether the next cluster must restate the whole style
	shown := Default // the foreground colour in effect when stale is false
	col = 0
	for rest := s; rest != ""; {
		var p piece
		p, rest = m.nextPiece(rest)
		switch {
		case p.kind != Plain:
			out = append(out, p.text...)
			if p.kind == CSI && p.complete && isSGR(p.text) {
				on, stale = true, true
			}
			continue
		case isNewline(p.text):
			if on {
				out = append(out, Reset...)
				on, stale = false, true
			}
			out = append(out, p.text...)
			col = 0
			continue
		case p.width == 0:
			// A control character or a stray mark: nothing to colour.
			out = append(out, p.text...)
			if p.inner {
				on, stale = true, true
			}
			continue
		case o.SkipSpaces && isBlank(p.text):
			if on {
				out = append(out, Reset...)
				on, stale = false, true
			}
			out = append(out, p.text...)
			col += p.width
			continue
		}
		// The cluster's colour is the gradient's at the middle of the
		// cluster, with the first and the last cell at the two ends.
		at := 0.0
		if span > 1 {
			at = (float64(col) + float64(p.width-1)/2) / float64(span-1)
		}
		r, g, b := rampAt(ramp, at).rgb()
		c := profile.Convert(RGB(r, g, b))
		switch {
		case stale:
			out = o.Style.state(c).appendSequence(out)
		case c != shown:
			out = append(out, "\x1b["...)
			out = appendColor(out, c, 30)
			out = append(out, 'm')
		}
		on, stale, shown = true, false, c
		out = append(out, p.text...)
		if p.inner {
			// Sequences inside the cluster may have changed the style.
			stale = true
		}
		col += p.width
	}
	if on {
		out = append(out, Reset...)
	}
	return string(out)
}

// Blend returns the colour a fraction t of the way from one colour to
// another: from at 0 and below, to at 1 and above, and between them the
// 24-bit colour that far along the straight line between the two in Oklab,
// as a gradient of the two would have it there (see GradientOptions.Render).
// ANSI and palette colours stand for their colours in the xterm palette.
// Default has no colour to blend: when either is Default the result is from
// below one half and to from there on.
func Blend(from, to Color, t float64) Color {
	from, to = from.valid(), to.valid()
	switch {
	case !(t > 0): // also when t is not a number
		return from
	case t >= 1:
		return to
	}
	fr, fg, fb, fromOK := from.rgb()
	tr, tg, tb, toOK := to.rgb()
	if !fromOK || !toOK {
		if t < 0.5 {
			return from
		}
		return to
	}
	return RGB(toLab(fr, fg, fb).blend(toLab(tr, tg, tb), t).rgb())
}

// rampAt returns the colour at position at, from 0 to 1, of a gradient whose
// stops are spread evenly.
func rampAt(ramp []lab, at float64) lab {
	last := len(ramp) - 1
	at = min(max(at, 0), 1) * float64(last)
	i := min(int(at), last)
	if i == last {
		return ramp[last]
	}
	return ramp[i].blend(ramp[i+1], at-float64(i))
}

// isBlank reports whether a cluster is a single white space character.
func isBlank(cluster string) bool {
	r, size := utf8.DecodeRuneInString(cluster)
	return size == len(cluster) && unicode.IsSpace(r)
}
