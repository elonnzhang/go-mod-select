package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The old banner was 92 columns of ASCII art with a stray apostrophe at the start
// of every row. On an 80-column terminal each row wrapped, and where it did fit
// the rows were centred one by one, so the art came out skewed.
func TestBannerIsACompactBlock(t *testing.T) {
	lines := strings.Split(banner, "\n")

	if len(lines) > 5 {
		t.Errorf("banner is %d rows; the analyzing screen shares its half of the terminal with a status block", len(lines))
	}
	if w := lipgloss.Width(banner); w > 60 {
		t.Errorf("banner is %d columns wide, too wide to be comfortable on an 80-column terminal", w)
	}
	for i, l := range lines {
		if lipgloss.Width(l) != lipgloss.Width(lines[0]) {
			t.Errorf("row %d is %d columns, row 0 is %d: rows must be padded to one width or centring skews them",
				i, lipgloss.Width(l), lipgloss.Width(lines[0]))
		}
		for _, r := range l {
			if r != ' ' && (r < 0x2500 || r > 0x257F) {
				t.Errorf("row %d contains %q; the banner is drawn with box-drawing characters only", i, r)
			}
		}
	}
}

func TestBannerStaysAlignedAndWithinTheTerminal(t *testing.T) {
	for _, w := range []int{50, 60, 80, 100, 160} {
		m := AppModel{width: w, colors: semverModel(0).colors}
		out := m.renderBanner()

		lines := strings.Split(out, "\n")
		if len(lines) != len(strings.Split(banner, "\n")) {
			t.Errorf("width %d: banner rendered %d rows (it wrapped)", w, len(lines))
			continue
		}
		indent := len(lines[0]) - len(strings.TrimLeft(lines[0], " "))
		for i, l := range lines {
			if lipgloss.Width(l) > w {
				t.Errorf("width %d row %d is %d columns", w, i, lipgloss.Width(l))
			}
			if got := len(l) - len(strings.TrimLeft(l, " ")); got != indent {
				t.Errorf("width %d row %d is indented %d, row 0 %d: the art is skewed", w, i, got, indent)
			}
		}
	}
}

func TestBannerFallsBackToATitleOnATinyTerminal(t *testing.T) {
	m := AppModel{width: 30, colors: semverModel(0).colors}

	out := m.renderBanner()

	if !strings.Contains(out, fallbackTitle) || strings.Contains(out, "╔") || lipgloss.Height(out) != 1 {
		t.Errorf("a %d-column terminal cannot hold the art; want the plain title, got:\n%s", m.width, out)
	}
}

func TestBlockOfPadsRowsAndTrimsTheEnds(t *testing.T) {
	got := blockOf("\n\nab\nabcd  \nx\n\n")

	want := "ab  \nabcd\nx   "
	if got != want {
		t.Errorf("blockOf = %q, want %q", got, want)
	}
}
