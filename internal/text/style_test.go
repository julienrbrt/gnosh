package text

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestStyleRender(t *testing.T) {
	pink := RGB(0xff, 0x5f, 0xd7)
	tests := []struct {
		name  string
		style Style
		text  string
		want  string
	}{
		{"no style", Style{}, "hi", "hi"},
		{"empty text", Style{}.Bold(), "", ""},
		{"bold", Style{}.Bold(), "hi", "\x1b[1mhi\x1b[0m"},
		{"dim", Style{}.Dim(), "hi", "\x1b[2mhi\x1b[0m"},
		{"italic", Style{}.Italic(), "hi", "\x1b[3mhi\x1b[0m"},
		{"underline", Style{}.Underline(), "hi", "\x1b[4mhi\x1b[0m"},
		{"reverse", Style{}.Reverse(), "hi", "\x1b[7mhi\x1b[0m"},
		{"strikethrough", Style{}.Strikethrough(), "hi", "\x1b[9mhi\x1b[0m"},
		{"every attribute", Style{}.Reverse().Strikethrough().Underline().Italic().Dim().Bold(), "hi", "\x1b[1;2;3;4;7;9mhi\x1b[0m"},
		{"ansi foreground", Style{}.Fg(Red), "hi", "\x1b[31mhi\x1b[0m"},
		{"bright foreground", Style{}.Fg(BrightRed), "hi", "\x1b[91mhi\x1b[0m"},
		{"black is a colour", Style{}.Fg(Black), "hi", "\x1b[30mhi\x1b[0m"},
		{"ansi background", Style{}.Bg(Blue), "hi", "\x1b[44mhi\x1b[0m"},
		{"bright background", Style{}.Bg(BrightWhite), "hi", "\x1b[107mhi\x1b[0m"},
		{"palette foreground", Style{}.Fg(Indexed(206)), "hi", "\x1b[38;5;206mhi\x1b[0m"},
		{"palette background", Style{}.Bg(Indexed(17)), "hi", "\x1b[48;5;17mhi\x1b[0m"},
		{"rgb foreground", Style{}.Fg(pink), "hi", "\x1b[38;2;255;95;215mhi\x1b[0m"},
		{"rgb background", Style{}.Bg(RGB(1, 2, 3)), "hi", "\x1b[48;2;1;2;3mhi\x1b[0m"},
		{"the brief's example", Style{}.Fg(pink).Bold(), "hi", "\x1b[1;38;2;255;95;215mhi\x1b[0m"},
		{"everything", Style{}.Fg(pink).Bg(Indexed(17)).Bold().Underline(), "hi", "\x1b[1;4;38;2;255;95;215;48;5;17mhi\x1b[0m"},
		{"colour taken off again", Style{}.Fg(Red).Bg(Blue).Fg(Default), "hi", "\x1b[44mhi\x1b[0m"},
		{"a colour that is not one", Style{}.Fg(Color(0x7f000001)).Bold(), "hi", "\x1b[1mhi\x1b[0m"},

		// Each line is styled on its own.
		{"two lines", Style{}.Bold(), "a\nb", "\x1b[1ma\x1b[0m\n\x1b[1mb\x1b[0m"},
		{"empty line between", Style{}.Bold(), "a\n\nb", "\x1b[1ma\x1b[0m\n\n\x1b[1mb\x1b[0m"},
		{"trailing newline", Style{}.Bold(), "a\n", "\x1b[1ma\x1b[0m\n"},
		{"leading newline", Style{}.Bold(), "\na", "\n\x1b[1ma\x1b[0m"},
		{"only newlines", Style{}.Bold(), "\n\n", "\n\n"},
		{"crlf", Style{}.Bold(), "a\r\nb", "\x1b[1ma\r\x1b[0m\n\x1b[1mb\x1b[0m"},

		// A style holds other styled strings.
		{"inner style in the middle", Style{}.Fg(Red), "a " + Style{}.Bold().Render("b") + " c", "\x1b[31ma \x1b[1mb\x1b[0m\x1b[31m c\x1b[0m"},
		{"inner style at the start", Style{}.Fg(Red), Style{}.Bold().Render("b") + " c", "\x1b[31m\x1b[1mb\x1b[0m\x1b[31m c\x1b[0m"},
		{"inner style at the end", Style{}.Fg(Red), "a " + Style{}.Bold().Render("b"), "\x1b[31ma \x1b[1mb\x1b[0m"},
		{"inner style is all there is", Style{}.Fg(Red), Style{}.Bold().Render("b"), "\x1b[31m\x1b[1mb\x1b[0m"},
		{"two inner styles", Style{}.Dim(), Style{}.Fg(Red).Render("a") + Style{}.Fg(Blue).Render("b"), "\x1b[2m\x1b[31ma\x1b[0m\x1b[2m\x1b[34mb\x1b[0m"},
		{"reset with more after it", Style{}.Fg(Red), "a\x1b[0;1mb", "\x1b[31ma\x1b[0m\x1b[31m\x1b[1mb\x1b[0m"},
		{"reset without parameters", Style{}.Fg(Red), "a\x1b[mb", "\x1b[31ma\x1b[0m\x1b[31mb\x1b[0m"},
		{"palette colour zero is not a reset", Style{}.Bold(), "a\x1b[38;5;0mb", "\x1b[1ma\x1b[38;5;0mb\x1b[0m"},
		{"other sequences pass through", Style{}.Fg(Red), "a\x1b[Kb", "\x1b[31ma\x1b[Kb\x1b[0m"},
		{"a link inside", Style{}.Underline(), Hyperlink("https://example.com", "x"), "\x1b]8;;https://example.com\x1b\\\x1b[4mx\x1b]8;;\x1b\\\x1b[0m"},
		{"inner style across lines", Style{}.Fg(Red), "a\x1b[1mb\nc\x1b[0md", "\x1b[31ma\x1b[1mb\x1b[0m\n\x1b[31mc\x1b[0m\x1b[31md\x1b[0m"},
	}
	for _, tt := range tests {
		if got := tt.style.Render(tt.text); got != tt.want {
			t.Errorf("%s: Render(%q)\n got %q\nwant %q", tt.name, tt.text, got, tt.want)
		}
	}
}

func TestStyleProfiles(t *testing.T) {
	pink := Style{}.Fg(RGB(0xff, 0x5f, 0xd7)).Bold()
	both := Style{}.Fg(RGB(250, 128, 114)).Bg(RGB(40, 44, 52)).Underline()
	tests := []struct {
		name    string
		style   Style
		profile Profile
		want    string
	}{
		{"truecolor", pink, TrueColor, "\x1b[1;38;2;255;95;215mhi\x1b[0m"},
		{"256", pink, ANSI256, "\x1b[1;38;5;206mhi\x1b[0m"},
		{"16", pink, ANSI16, "\x1b[1;95mhi\x1b[0m"},
		{"none", pink, NoColor, "hi"},
		{"two colours, truecolor", both, TrueColor, "\x1b[4;38;2;250;128;114;48;2;40;44;52mhi\x1b[0m"},
		{"two colours, 256", both, ANSI256, "\x1b[4;38;5;210;48;5;236mhi\x1b[0m"},
		{"two colours, 16", both, ANSI16, "\x1b[4;91;40mhi\x1b[0m"},
		{"two colours, none", both, NoColor, "hi"},
		{"palette colour under 16", Style{}.Fg(Indexed(196)), ANSI16, "\x1b[91mhi\x1b[0m"},
		{"low palette colour under 16", Style{}.Bg(Indexed(4)), ANSI16, "\x1b[44mhi\x1b[0m"},
		{"palette colour under 256", Style{}.Fg(Indexed(9)), ANSI256, "\x1b[38;5;9mhi\x1b[0m"},
		{"ansi colour under 256", Style{}.Fg(BrightCyan), ANSI256, "\x1b[96mhi\x1b[0m"},
		{"ansi colour under truecolor", Style{}.Fg(Cyan), TrueColor, "\x1b[36mhi\x1b[0m"},
		{"attributes alone under none", Style{}.Bold().Underline(), NoColor, "hi"},
		{"attributes alone under 16", Style{}.Bold().Underline(), ANSI16, "\x1b[1;4mhi\x1b[0m"},
		{"a profile that does not exist", pink, Profile(99), "\x1b[1;38;2;255;95;215mhi\x1b[0m"},
	}
	for _, tt := range tests {
		if got := tt.style.In(tt.profile).Render("hi"); got != tt.want {
			t.Errorf("%s: under %v Render gives %q, want %q", tt.name, tt.profile, got, tt.want)
		}
	}

	// The order of the calls does not matter, and a style remembers the
	// colours it was given.
	other := Style{}.In(ANSI16).Fg(RGB(0xff, 0x5f, 0xd7)).Bold()
	if pink.In(ANSI16) != other {
		t.Errorf("In then Fg differs from Fg then In: %+v, %+v", other, pink.In(ANSI16))
	}
	if got := pink.In(ANSI16).In(TrueColor); got != pink.In(TrueColor) {
		t.Errorf("a style set to 16 colours and back lost its colour: %+v", got)
	}
	if got, want := pink.In(NoColor).In(ANSI256).Render("hi"), "\x1b[1;38;5;206mhi\x1b[0m"; got != want {
		t.Errorf("a style set to no colour and then to 256 gives %q, want %q", got, want)
	}
	// Multi-line and nested rendering under NoColor leave the text alone.
	if got := pink.In(NoColor).Render("a\nb\x1b[0mc"); got != "a\nb\x1b[0mc" {
		t.Errorf("under NoColor Render changed the text to %q", got)
	}
}

func TestStyleSequence(t *testing.T) {
	tests := []struct {
		style Style
		want  string
	}{
		{Style{}, ""},
		{Style{}.Bold(), "\x1b[1m"},
		{Style{}.Fg(RGB(0xff, 0x5f, 0xd7)).Bold(), "\x1b[1;38;2;255;95;215m"},
		{Style{}.Fg(RGB(0xff, 0x5f, 0xd7)).Bold().In(ANSI256), "\x1b[1;38;5;206m"},
		{Style{}.Fg(Red).Bold().In(NoColor), ""},
		{Style{}.Fg(RGB(255, 255, 255)).Bg(RGB(255, 255, 255)).Bold().Dim().Italic().Underline().Reverse().Strikethrough(), "\x1b[1;2;3;4;7;9;38;2;255;255;255;48;2;255;255;255m"},
	}
	for _, tt := range tests {
		if got := tt.style.Sequence(); got != tt.want {
			t.Errorf("%+v.Sequence() = %q, want %q", tt.style, got, tt.want)
		}
	}
	if Reset != "\x1b[0m" {
		t.Errorf("Reset is %q", Reset)
	}
}

func TestColor(t *testing.T) {
	tests := []struct {
		c       Color
		name    string
		r, g, b uint8
		ok      bool
	}{
		{Default, "default", 0, 0, 0, false},
		{Black, "ansi:0", 0, 0, 0, true},
		{Red, "ansi:1", 205, 0, 0, true},
		{BrightBlue, "ansi:12", 92, 92, 255, true},
		{BrightWhite, "ansi:15", 255, 255, 255, true},
		{Indexed(0), "indexed:0", 0, 0, 0, true},
		{Indexed(206), "indexed:206", 255, 95, 215, true},
		{Indexed(244), "indexed:244", 128, 128, 128, true},
		{RGB(0xff, 0x5f, 0xd7), "#ff5fd7", 255, 95, 215, true},
		{RGB(0, 0, 0), "#000000", 0, 0, 0, true},
		{Color(0x7f000001), "default", 0, 0, 0, false},
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.name {
			t.Errorf("Color(%#x).String() = %q, want %q", uint32(tt.c), got, tt.name)
		}
		r, g, b, ok := tt.c.rgb()
		if r != tt.r || g != tt.g || b != tt.b || ok != tt.ok {
			t.Errorf("%v.rgb() = %d,%d,%d,%t; want %d,%d,%d,%t", tt.c, r, g, b, ok, tt.r, tt.g, tt.b, tt.ok)
		}
	}
	// The named colours are the sixteen ANSI colours in order.
	named := []Color{Black, Red, Green, Yellow, Blue, Magenta, Cyan, White,
		BrightBlack, BrightRed, BrightGreen, BrightYellow, BrightBlue, BrightMagenta, BrightCyan, BrightWhite}
	for i, c := range named {
		if c != ansiColor(i) {
			t.Errorf("named colour %d is %v", i, c)
		}
	}
	if Black == Default || RGB(0, 0, 0) == Black || Indexed(0) == Black {
		t.Error("black, palette colour 0, RGB black and Default must all differ")
	}
}

// paletteInOklab converts the whole palette the slow, plain way, in floating
// point, from its RGB values.
func paletteInOklab() (palette [256]lab) {
	for i := range palette {
		palette[i] = toLab(paletteRGB(i))
	}
	return palette
}

// checkNearest checks that got is the palette colour, among those numbered
// first up to end, that is nearest to c. The package searches a table of
// coordinates rounded to one part in 30000, so of two colours that are
// equally near to within that rounding it may pick either.
func checkNearest(t *testing.T, palette *[256]lab, c Color, got Color, first, end int) {
	t.Helper()
	if k := got.kind(); k != colorANSI && k != colorIndexed {
		t.Errorf("%v converts to %v, which is not a palette colour", c, got)
		return
	}
	index := int(got & 0xFF)
	if index < first || index >= end {
		t.Errorf("%v converts to %v, outside colours %d to %d", c, got, first, end-1)
		return
	}
	r, g, b, _ := c.rgb()
	want := toLab(r, g, b)
	distance := func(i int) float64 {
		dl, da, db := palette[i].l-want.l, palette[i].a-want.a, palette[i].b-want.b
		return math.Sqrt(dl*dl + da*da + db*db)
	}
	best, bestDistance := first, distance(first)
	for i := first + 1; i < end; i++ {
		if d := distance(i); d < bestDistance {
			best, bestDistance = i, d
		}
	}
	const rounding = 1e-4
	if d := distance(index); d > bestDistance+rounding {
		t.Errorf("%v converts to %v at distance %.5f, but colour %d is nearer at %.5f", c, got, d, best, bestDistance)
	}
}

func TestProfileConvert(t *testing.T) {
	// Values worked out separately from the definition of Oklab.
	tests := []struct {
		c      Color
		to256  Color
		to16   Color
		reason string
	}{
		{RGB(255, 95, 215), Indexed(206), BrightMagenta, "gnosh's pink is a palette colour"},
		{RGB(0, 0, 0), Indexed(16), Black, "black"},
		{RGB(255, 255, 255), Indexed(231), BrightWhite, "white"},
		{RGB(128, 128, 128), Indexed(244), BrightBlack, "mid grey is on the grey ramp"},
		{RGB(255, 0, 0), Indexed(196), BrightRed, "pure red"},
		{RGB(0, 255, 0), Indexed(46), BrightGreen, "pure green"},
		{RGB(0, 0, 255), Indexed(21), Blue, "pure blue"},
		{RGB(40, 44, 52), Indexed(236), Black, "a dark blue-grey goes to a grey"},
		{RGB(95, 135, 175), Indexed(67), BrightBlack, "a cube colour"},
		{RGB(250, 128, 114), Indexed(210), BrightRed, "salmon"},
		{RGB(1, 2, 3), Indexed(232), Black, "nearly black goes to the darkest grey"},
		{RGB(255, 165, 0), Indexed(214), Yellow, "orange"},
		{RGB(139, 69, 19), Indexed(94), Red, "brown"},
		{RGB(200, 200, 200), Indexed(251), White, "light grey"},
		{Indexed(206), Indexed(206), BrightMagenta, "a palette colour stays under 256"},
		{Indexed(17), Indexed(17), Blue, "dark blue"},
		{Indexed(244), Indexed(244), BrightBlack, "a grey"},
		{Indexed(9), Indexed(9), BrightRed, "a low palette colour is the ANSI colour"},
		{Indexed(0), Indexed(0), Black, "palette colour 0"},
		{Red, Red, Red, "an ANSI colour stays"},
		{BrightCyan, BrightCyan, BrightCyan, "a bright ANSI colour stays"},
		{Default, Default, Default, "the default stays"},
	}
	for _, tt := range tests {
		if got := TrueColor.Convert(tt.c); got != tt.c {
			t.Errorf("%s: TrueColor.Convert(%v) = %v", tt.reason, tt.c, got)
		}
		if got := ANSI256.Convert(tt.c); got != tt.to256 {
			t.Errorf("%s: ANSI256.Convert(%v) = %v, want %v", tt.reason, tt.c, got, tt.to256)
		}
		if got := ANSI16.Convert(tt.c); got != tt.to16 {
			t.Errorf("%s: ANSI16.Convert(%v) = %v, want %v", tt.reason, tt.c, got, tt.to16)
		}
		if got := NoColor.Convert(tt.c); got != Default {
			t.Errorf("%s: NoColor.Convert(%v) = %v", tt.reason, tt.c, got)
		}
	}

	// Every palette colour, given as RGB, converts to itself or to an
	// earlier colour with the same value (the cube and the ramp share
	// black, white and a few greys).
	for i := 16; i < 256; i++ {
		r, g, b := paletteRGB(i)
		got := ANSI256.Convert(RGB(r, g, b))
		gr, gg, gb, _ := got.rgb()
		if gr != r || gg != g || gb != b || int(got&0xFF) > i {
			t.Errorf("palette colour %d (%d,%d,%d) converts to %v", i, r, g, b, got)
		}
	}
	for i := range 16 {
		r, g, b := paletteRGB(i)
		if got := ANSI16.Convert(RGB(r, g, b)); got != ansiColor(i) {
			t.Errorf("ANSI colour %d (%d,%d,%d) converts to %v", i, r, g, b, got)
		}
	}

	// Over a grid of colours, the conversion picks the nearest colour as
	// the definition has it.
	palette := paletteInOklab()
	for r := 0; r < 256; r += 15 {
		for g := 0; g < 256; g += 15 {
			for b := 0; b < 256; b += 15 {
				c := RGB(uint8(r), uint8(g), uint8(b))
				checkNearest(t, &palette, c, ANSI256.Convert(c), 16, 256)
				checkNearest(t, &palette, c, ANSI16.Convert(c), 0, 16)
			}
		}
	}
	for i := 16; i < 256; i++ {
		checkNearest(t, &palette, Indexed(uint8(i)), ANSI16.Convert(Indexed(uint8(i))), 0, 16)
	}
}

func TestOklab(t *testing.T) {
	// White is lightness 1 with no colour; black is the origin.
	if w := toLab(255, 255, 255); math.Abs(w.l-1) > 1e-4 || math.Abs(w.a) > 1e-4 || math.Abs(w.b) > 1e-4 {
		t.Errorf("white is %+v in Oklab", w)
	}
	if k := toLab(0, 0, 0); k != (lab{}) {
		t.Errorf("black is %+v in Oklab", k)
	}
	// The values Ottosson's article gives for pure red.
	if r := toLab(255, 0, 0); math.Abs(r.l-0.628) > 1e-3 || math.Abs(r.a-0.225) > 1e-3 || math.Abs(r.b-0.126) > 1e-3 {
		t.Errorf("red is %+v in Oklab, want about {0.628 0.225 0.126}", r)
	}
	// Every colour survives the trip there and back.
	for r := 0; r < 256; r += 5 {
		for g := 0; g < 256; g += 5 {
			for b := 0; b < 256; b += 5 {
				gr, gg, gb := toLab(uint8(r), uint8(g), uint8(b)).rgb()
				if int(gr) != r || int(gg) != g || int(gb) != b {
					t.Fatalf("%d,%d,%d comes back from Oklab as %d,%d,%d", r, g, b, gr, gg, gb)
				}
			}
		}
	}
	// Colours no screen can show are clipped, not wrapped around.
	if r, g, b := (lab{l: 2}).rgb(); r != 255 || g != 255 || b != 255 {
		t.Errorf("a colour brighter than white comes out as %d,%d,%d", r, g, b)
	}
	if r, g, b := (lab{l: -1}).rgb(); r != 0 || g != 0 || b != 0 {
		t.Errorf("a colour darker than black comes out as %d,%d,%d", r, g, b)
	}
}

func TestDetectProfile(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		isTTY bool
		want  Profile
	}{
		// The terminal's own variables.
		{"nothing set", nil, true, NoColor},
		{"xterm", map[string]string{"TERM": "xterm"}, true, ANSI16},
		{"vt100", map[string]string{"TERM": "vt100"}, true, ANSI16},
		{"linux console", map[string]string{"TERM": "linux"}, true, ANSI16},
		{"screen", map[string]string{"TERM": "screen"}, true, ANSI16},
		{"xterm-256color", map[string]string{"TERM": "xterm-256color"}, true, ANSI256},
		{"tmux-256color", map[string]string{"TERM": "tmux-256color"}, true, ANSI256},
		{"screen.xterm-256color", map[string]string{"TERM": "screen.xterm-256color"}, true, ANSI256},
		{"upper case", map[string]string{"TERM": "XTERM-256COLOR"}, true, ANSI256},
		{"xterm-direct", map[string]string{"TERM": "xterm-direct"}, true, TrueColor},
		{"kitty", map[string]string{"TERM": "xterm-kitty"}, true, TrueColor},
		{"ghostty", map[string]string{"TERM": "xterm-ghostty"}, true, TrueColor},
		{"alacritty", map[string]string{"TERM": "alacritty"}, true, TrueColor},
		{"wezterm", map[string]string{"TERM": "wezterm"}, true, TrueColor},
		{"foot", map[string]string{"TERM": "foot"}, true, TrueColor},
		{"contour", map[string]string{"TERM": "contour"}, true, TrueColor},
		{"colorterm truecolor", map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}, true, TrueColor},
		{"colorterm 24bit", map[string]string{"TERM": "xterm", "COLORTERM": "24bit"}, true, TrueColor},
		{"colorterm in upper case", map[string]string{"TERM": "xterm", "COLORTERM": "TrueColor"}, true, TrueColor},
		{"colorterm without term", map[string]string{"COLORTERM": "truecolor"}, true, TrueColor},
		{"colorterm of another kind", map[string]string{"COLORTERM": "yes"}, true, ANSI16},
		{"colorterm of another kind with 256", map[string]string{"TERM": "xterm-256color", "COLORTERM": "1"}, true, ANSI256},
		{"empty term", map[string]string{"TERM": ""}, true, NoColor},

		// Not a terminal, and terminals that take no colour.
		{"not a terminal", map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}, false, NoColor},
		{"dumb", map[string]string{"TERM": "dumb"}, true, NoColor},
		{"dumb with colorterm", map[string]string{"TERM": "dumb", "COLORTERM": "truecolor"}, true, NoColor},
		{"clicolor off", map[string]string{"TERM": "xterm-256color", "CLICOLOR": "0"}, true, NoColor},
		{"clicolor on changes nothing", map[string]string{"TERM": "xterm-256color", "CLICOLOR": "1"}, true, ANSI256},
		{"clicolor on does not force", map[string]string{"TERM": "xterm-256color", "CLICOLOR": "1"}, false, NoColor},

		// NO_COLOR.
		{"no_color", map[string]string{"TERM": "xterm-256color", "NO_COLOR": "1"}, true, NoColor},
		{"no_color with any value", map[string]string{"TERM": "xterm-256color", "NO_COLOR": "false"}, true, NoColor},
		{"no_color empty is not set", map[string]string{"TERM": "xterm-256color", "NO_COLOR": ""}, true, ANSI256},
		{"no_color beats force_color", map[string]string{"TERM": "xterm", "NO_COLOR": "1", "FORCE_COLOR": "3"}, true, NoColor},
		{"no_color beats clicolor_force", map[string]string{"TERM": "xterm", "NO_COLOR": "1", "CLICOLOR_FORCE": "1"}, false, NoColor},
		{"no_color beats colorterm", map[string]string{"COLORTERM": "truecolor", "NO_COLOR": "1"}, true, NoColor},

		// Forcing colour.
		{"force_color off", map[string]string{"TERM": "xterm-256color", "FORCE_COLOR": "0"}, true, NoColor},
		{"force_color false", map[string]string{"TERM": "xterm-256color", "FORCE_COLOR": "false"}, true, NoColor},
		{"force_color off beats clicolor_force", map[string]string{"TERM": "xterm", "FORCE_COLOR": "0", "CLICOLOR_FORCE": "1"}, true, NoColor},
		{"force_color empty is not set", map[string]string{"TERM": "xterm", "FORCE_COLOR": ""}, false, NoColor},
		{"force_color into a pipe", map[string]string{"FORCE_COLOR": "1"}, false, ANSI16},
		{"force_color true", map[string]string{"FORCE_COLOR": "true"}, false, ANSI16},
		{"force_color any word", map[string]string{"FORCE_COLOR": "yes"}, false, ANSI16},
		{"force_color 2", map[string]string{"FORCE_COLOR": "2"}, false, ANSI256},
		{"force_color 3", map[string]string{"FORCE_COLOR": "3"}, false, TrueColor},
		{"force_color keeps a richer terminal", map[string]string{"TERM": "xterm-256color", "FORCE_COLOR": "1"}, false, ANSI256},
		{"force_color 2 keeps a richer terminal", map[string]string{"TERM": "xterm-kitty", "FORCE_COLOR": "2"}, false, TrueColor},
		{"force_color 3 over a plain terminal", map[string]string{"TERM": "xterm", "FORCE_COLOR": "3"}, true, TrueColor},
		{"force_color on a dumb terminal", map[string]string{"TERM": "dumb", "FORCE_COLOR": "1"}, true, ANSI16},
		{"force_color beats clicolor off", map[string]string{"TERM": "xterm", "CLICOLOR": "0", "FORCE_COLOR": "1"}, true, ANSI16},
		{"clicolor_force into a pipe", map[string]string{"CLICOLOR_FORCE": "1"}, false, ANSI16},
		{"clicolor_force with a terminal's variables", map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor", "CLICOLOR_FORCE": "1"}, false, TrueColor},
		{"clicolor_force zero is not set", map[string]string{"TERM": "xterm", "CLICOLOR_FORCE": "0"}, false, NoColor},
		{"clicolor_force empty is not set", map[string]string{"TERM": "xterm", "CLICOLOR_FORCE": ""}, false, NoColor},
	}
	for _, tt := range tests {
		lookup := func(name string) (string, bool) {
			value, ok := tt.env[name]
			return value, ok
		}
		if got := DetectProfile(lookup, tt.isTTY); got != tt.want {
			t.Errorf("%s: DetectProfile(%v, %t) = %v, want %v", tt.name, tt.env, tt.isTTY, got, tt.want)
		}
	}
	if got := DetectProfile(nil, true); got != NoColor {
		t.Errorf("DetectProfile with no lookup = %v, want none", got)
	}
	names := map[Profile]string{NoColor: "none", ANSI16: "16", ANSI256: "256", TrueColor: "truecolor", Profile(9): "none"}
	for p, want := range names {
		if got := p.String(); got != want {
			t.Errorf("Profile(%d).String() = %q, want %q", p, got, want)
		}
	}
	if !(NoColor < ANSI16 && ANSI16 < ANSI256 && ANSI256 < TrueColor) {
		t.Error("the profiles are not in order of how much colour they allow")
	}
	var zero Profile
	if zero != NoColor {
		t.Error("the zero Profile is not NoColor")
	}
}

func TestHyperlink(t *testing.T) {
	const open = "\x1b]8;;https://example.com\x1b\\"
	const shut = "\x1b]8;;\x1b\\"
	tests := []struct {
		name      string
		url, text string
		want      string
	}{
		{"plain", "https://example.com", "link", open + "link" + shut},
		{"empty url", "", "link", "link"},
		{"empty text", "https://example.com", "", ""},
		{"styled text", "https://example.com", "\x1b[4mlink\x1b[0m", "\x1b[4m" + open + "link\x1b[0m" + shut},
		{"two lines", "https://example.com", "a\nb", open + "a" + shut + "\n" + open + "b" + shut},
		{"trailing newline", "https://example.com", "a\n", open + "a" + shut + "\n"},
		{"query and fragment", "https://example.com/a?b=c&d=e#f;g", "x", "\x1b]8;;https://example.com/a?b=c&d=e#f;g\x1b\\x" + shut},
		{"space in the url", "https://example.com/a b", "x", "\x1b]8;;https://example.com/a%20b\x1b\\x" + shut},
		{"control characters in the url", "https://example.com/\x1b\\\x07\n", "x", "\x1b]8;;https://example.com/%1B\\%07%0A\x1b\\x" + shut},
		{"non-ascii url", "https://example.com/é", "x", "\x1b]8;;https://example.com/%C3%A9\x1b\\x" + shut},
		{"percent escapes are left alone", "https://example.com/a%20b", "x", "\x1b]8;;https://example.com/a%20b\x1b\\x" + shut},
		{"a link inside takes over", "https://example.com", "a\x1b]8;;https://example.org\x1b\\b\x1b]8;;\x1b\\c",
			open + "a\x1b]8;;https://example.org\x1b\\b\x1b]8;;\x1b\\" + open + "c" + shut},
	}
	for _, tt := range tests {
		got := Hyperlink(tt.url, tt.text)
		if got != tt.want {
			t.Errorf("%s: Hyperlink(%q, %q)\n got %q\nwant %q", tt.name, tt.url, tt.text, got, tt.want)
		}
		if Strip(got) != Strip(tt.text) {
			t.Errorf("%s: the link shows %q, not %q", tt.name, Strip(got), Strip(tt.text))
		}
		for tk := range Tokens(got) {
			if !tk.Complete {
				t.Errorf("%s: the link holds an unfinished sequence %q", tt.name, tk.Text)
			}
		}
	}
	if got := NoColor.Hyperlink("https://example.com", "link"); got != "link" {
		t.Errorf("NoColor.Hyperlink = %q, want the text alone", got)
	}
	if got := ANSI16.Hyperlink("https://example.com", "link"); got != open+"link"+shut {
		t.Errorf("ANSI16.Hyperlink = %q", got)
	}
}

func TestGradient(t *testing.T) {
	red, blue := RGB(255, 0, 0), RGB(0, 0, 255)
	black, white := RGB(0, 0, 0), RGB(255, 255, 255)
	purple, pink := RGB(95, 0, 255), RGB(255, 95, 215)
	fg := func(r, g, b int) string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b) }
	tests := []struct {
		name  string
		opts  GradientOptions
		in    string
		stops []Color
		want  string
	}{
		{"empty", GradientOptions{}, "", []Color{red, blue}, ""},
		{"no stops", GradientOptions{}, "abc", nil, "abc"},
		{"only default stops", GradientOptions{}, "abc", []Color{Default, Default}, "abc"},
		{"one stop", GradientOptions{}, "abc", []Color{red}, fg(255, 0, 0) + "abc" + Reset},
		{"one character", GradientOptions{}, "a", []Color{red, blue}, fg(255, 0, 0) + "a" + Reset},
		{"ends are the stops", GradientOptions{}, "ab", []Color{red, blue}, fg(255, 0, 0) + "a" + fg(0, 0, 255) + "b" + Reset},
		{"blended in Oklab, not RGB", GradientOptions{}, "abc", []Color{black, white}, fg(0, 0, 0) + "a" + fg(99, 99, 99) + "b" + fg(255, 255, 255) + "c" + Reset},
		{"four steps", GradientOptions{}, "abcd", []Color{red, blue}, fg(255, 0, 0) + "a" + fg(179, 79, 128) + "b" + fg(101, 78, 194) + "c" + fg(0, 0, 255) + "d" + Reset},
		{"three stops", GradientOptions{}, "abc", []Color{red, RGB(0, 255, 0), blue}, fg(255, 0, 0) + "a" + fg(0, 255, 0) + "b" + fg(0, 0, 255) + "c" + Reset},
		{"three stops, five steps", GradientOptions{}, "abcde", []Color{red, RGB(0, 255, 0), blue},
			fg(255, 0, 0) + "a" + fg(208, 168, 0) + "b" + fg(0, 255, 0) + "c" + fg(0, 170, 191) + "d" + fg(0, 0, 255) + "e" + Reset},
		{"default stops are left out", GradientOptions{}, "ab", []Color{Default, red, Default, blue}, fg(255, 0, 0) + "a" + fg(0, 0, 255) + "b" + Reset},
		{"ansi and palette stops", GradientOptions{}, "ab", []Color{BrightRed, Indexed(21)}, fg(255, 0, 0) + "a" + fg(0, 0, 255) + "b" + Reset},
		{"spaces are coloured", GradientOptions{}, "a b", []Color{red, red}, fg(255, 0, 0) + "a b" + Reset},
		{"spaces skipped", GradientOptions{SkipSpaces: true}, "a b", []Color{red, blue}, fg(255, 0, 0) + "a" + Reset + " " + fg(0, 0, 255) + "b" + Reset},
		{"leading and trailing spaces skipped", GradientOptions{SkipSpaces: true}, " ab ", []Color{red, red}, " " + fg(255, 0, 0) + "ab" + Reset + " "},
		{"with a style", GradientOptions{Style: Style{}.Bold().Bg(Black)}, "ab", []Color{red, blue}, "\x1b[1;38;2;255;0;0;40ma\x1b[38;2;0;0;255mb" + Reset},
		{"the style's own foreground is not used", GradientOptions{Style: Style{}.Fg(Green)}, "ab", []Color{red, blue}, fg(255, 0, 0) + "a" + fg(0, 0, 255) + "b" + Reset},
		{"style and skipped spaces", GradientOptions{Style: Style{}.Underline(), SkipSpaces: true}, "a b", []Color{red, red},
			"\x1b[4;38;2;255;0;0ma" + Reset + " \x1b[4;38;2;255;0;0mb" + Reset},
		{"lines share their columns", GradientOptions{}, "ab\nabcd", []Color{red, blue},
			fg(255, 0, 0) + "a" + fg(179, 79, 128) + "b" + Reset + "\n" + fg(255, 0, 0) + "a" + fg(179, 79, 128) + "b" + fg(101, 78, 194) + "c" + fg(0, 0, 255) + "d" + Reset},
		{"trailing newline", GradientOptions{}, "ab\n", []Color{red, blue}, fg(255, 0, 0) + "a" + fg(0, 0, 255) + "b" + Reset + "\n"},
		{"wide characters", GradientOptions{}, "漢字", []Color{red, blue}, fg(217, 63, 87) + "漢" + fg(59, 61, 225) + "字" + Reset},
		{"clusters take one colour", GradientOptions{}, "e\u0301👍🏽", []Color{red, red}, fg(255, 0, 0) + "e\u0301👍🏽" + Reset},
		{"control characters take none", GradientOptions{}, "a\tb", []Color{red, blue}, fg(255, 0, 0) + "a\t" + fg(0, 0, 255) + "b" + Reset},
		{"sequences in the text are kept", GradientOptions{}, "a\x1b[1mb", []Color{red, red}, fg(255, 0, 0) + "a\x1b[1m" + fg(255, 0, 0) + "b" + Reset},

		// Lesser profiles.
		{"256 colours", GradientOptions{Style: Style{}.In(ANSI256)}, "0123456789", []Color{purple, pink},
			"\x1b[38;5;57m01\x1b[38;5;93m2\x1b[38;5;99m3\x1b[38;5;135m45\x1b[38;5;170m67\x1b[38;5;206m89" + Reset},
		{"16 colours", GradientOptions{Style: Style{}.In(ANSI16)}, "0123456789", []Color{purple, pink},
			"\x1b[94m0123\x1b[35m4567\x1b[95m89" + Reset},
		{"16 colours with a style", GradientOptions{Style: Style{}.Bold().In(ANSI16)}, "0123456789", []Color{purple, pink},
			"\x1b[1;94m0123\x1b[35m4567\x1b[95m89" + Reset},
		{"no colour", GradientOptions{Style: Style{}.Bold().In(NoColor)}, "0123456789", []Color{purple, pink}, "0123456789"},
	}
	for _, tt := range tests {
		got := tt.opts.Render(tt.in, tt.stops...)
		if got != tt.want {
			t.Errorf("%s: Render(%q)\n got %q\nwant %q", tt.name, tt.in, got, tt.want)
		}
		if Strip(got) != Strip(tt.in) {
			t.Errorf("%s: the gradient shows %q, not %q", tt.name, Strip(got), Strip(tt.in))
		}
	}
	if got, want := Gradient("ab", red, blue), fg(255, 0, 0)+"a"+fg(0, 0, 255)+"b"+Reset; got != want {
		t.Errorf("Gradient gives %q, want %q", got, want)
	}

	// gnosh's wordmark: purple to hot pink, each line standing alone.
	mark := "█▀▀ █▄ █ █▀█ █▀ █ █\n█▄█ █ ▀█ █▄█ ▄█ █▀█"
	out := GradientOptions{SkipSpaces: true}.Render(mark, purple, pink)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("the wordmark came out as %d lines", len(lines))
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, fg(95, 0, 255)+"█") || !strings.HasSuffix(line, fg(255, 95, 215)+"█"+Reset) {
			t.Errorf("line %d of the wordmark does not run from purple to pink: %q", i, line)
		}
		if l := endLook(line); !l.plain() {
			t.Errorf("line %d of the wordmark leaves a style open", i)
		}
	}
	if Strip(out) != mark {
		t.Errorf("the wordmark's text changed: %q", Strip(out))
	}
}

// FuzzStyle checks that rendering never changes what is shown, and that
// every line of a rendered string stands alone.
func FuzzStyle(f *testing.F) {
	for i, seed := range fuzzSeeds() {
		f.Add(seed, uint8(i))
	}
	f.Fuzz(func(t *testing.T, s string, pick uint8) {
		profile := Profile(pick % 4)
		style := Style{}.Fg(RGB(pick, 95, 215)).Bold().In(profile)
		if pick&4 != 0 {
			style = style.Bg(Indexed(pick)).Underline()
		}
		rendered := style.Render(s)
		if Strip(rendered) != Strip(s) {
			t.Fatalf("Render(%q) shows %q", s, Strip(rendered))
		}
		if profile == NoColor && rendered != s {
			t.Fatalf("under NoColor Render(%q) = %q", s, rendered)
		}
		if !hasUnfinished(s) {
			for _, tk := range strings.Split(rendered, "\n") {
				if l := endLook(tk); l.sgr != (sgrState{}) && !strings.Contains(s, "\x1b") {
					t.Fatalf("Render(%q) leaves a style open on the line %q", s, tk)
				}
			}
		}

		linked := Hyperlink("https://example.com/a b", s)
		if Strip(linked) != Strip(s) {
			t.Fatalf("Hyperlink(%q) shows %q", s, Strip(linked))
		}

		graded := GradientOptions{Style: style, SkipSpaces: pick&8 != 0}.Render(s, RGB(95, 0, 255), Indexed(pick), RGB(255, 95, 215))
		if Strip(graded) != Strip(s) {
			t.Fatalf("Gradient(%q) shows %q", s, Strip(graded))
		}
		if profile == NoColor && graded != s {
			t.Fatalf("under NoColor Gradient(%q) = %q", s, graded)
		}
	})
}

func TestBlend(t *testing.T) {
	red, blue := RGB(255, 0, 0), RGB(0, 0, 255)
	black, white := RGB(0, 0, 0), RGB(255, 255, 255)
	tests := []struct {
		name     string
		from, to Color
		t        float64
		want     Color
	}{
		{"the start", red, blue, 0, red},
		{"before the start", red, blue, -3, red},
		{"the end", red, blue, 1, blue},
		{"past the end", red, blue, 7, blue},
		{"not a number", red, blue, math.NaN(), red},
		// The same colours a gradient of the two has there: in Oklab, where
		// the middle of black and white is a middle grey.
		{"half way, in Oklab", black, white, 0.5, RGB(99, 99, 99)},
		{"a third", red, blue, 1.0 / 3, RGB(179, 79, 128)},
		{"two thirds", red, blue, 2.0 / 3, RGB(101, 78, 194)},
		{"a colour with itself", red, red, 0.4, red},
		{"ansi and palette colours by their xterm values", BrightRed, Indexed(21), 1.0 / 3, RGB(179, 79, 128)},
		{"the ends are given back as they were", BrightRed, Indexed(21), 1, Indexed(21)},
		// The terminal's own colour has no place to blend from.
		{"from the default, early", Default, blue, 0.49, Default},
		{"from the default, late", Default, blue, 0.5, blue},
		{"to the default, early", red, Default, 0.2, red},
		{"to the default, late", red, Default, 0.8, Default},
		{"a colour no constructor made", Color(0xFF000000), blue, 0.2, Default},
	}
	for _, tt := range tests {
		if got := Blend(tt.from, tt.to, tt.t); got != tt.want {
			t.Errorf("%s: Blend(%v, %v, %v) = %v, want %v", tt.name, tt.from, tt.to, tt.t, got, tt.want)
		}
	}
	// Along the way the lightness only rises, as a gradient's does (the
	// darkest steps round to the same eight bits).
	last, rises := 0.0, 0
	for i := 0; i <= 20; i++ {
		r, g, b, _ := Blend(black, white, float64(i)/20).rgb()
		l := toLab(r, g, b).l
		switch {
		case l < last:
			t.Fatalf("at %d/20 from black to white the lightness is %v, after %v", i, l, last)
		case l > last:
			rises++
		}
		last = l
	}
	if rises < 15 || last < 0.999 {
		t.Errorf("from black to white the lightness rose %d times in 20 steps, to %v", rises, last)
	}
}
