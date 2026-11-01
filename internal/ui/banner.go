// Banner merender logo unicode (block elements) untuk root command.
package ui

import (
	"strings"
)

// letterGrid: setiap huruf AVIOR dalam grid 6 baris x 8 kolom, stroke tebal.
var letterGrid = map[rune][6]string{
	'A': {
		"  █████  ",
		" ██   ██ ",
		"██     ██",
		"█████████",
		"██     ██",
		"██     ██",
	},
	'V': {
		"██     ██",
		"██     ██",
		"██     ██",
		" ██   ██ ",
		"  ██ ██  ",
		"   ███   ",
	},
	'I': {
		"████████ ",
		"   ██    ",
		"   ██    ",
		"   ██    ",
		"   ██    ",
		"████████ ",
	},
	'O': {
		" ███████ ",
		"██     ██",
		"██     ██",
		"██     ██",
		"██     ██",
		" ███████ ",
	},
	'R': {
		"███████  ",
		"██     ██",
		"██     ██",
		"███████  ",
		"██  ██   ",
		"██   ██  ",
	},
}

func bannerLines() []string {
	word := "AVIOR"
	letters := make([][6]string, len(word))
	for i, r := range word {
		g, ok := letterGrid[r]
		if !ok {
			g = [6]string{"         ", "         ", "         ", "         ", "         ", "         "}
		}
		letters[i] = g
	}
	rows := make([]string, 6)
	for row := 0; row < 6; row++ {
		var b strings.Builder
		for i := range letters {
			b.WriteString(letters[i][row])
			if i < len(letters)-1 {
				b.WriteString("  ")
			}
		}
		rows[row] = strings.TrimRight(b.String(), " ")
	}
	return rows
}

// Banner mengembalikan logo (unicode, tebal) + tagline + "By MaleoLabs".
// Warna otomatis non-aktif di non-TTY (NO_COLOR/pipe).
func Banner() string {
	var b strings.Builder
	colors := []Color{Primary, Info, Success, Warning, Primary}
	for i, line := range bannerLines() {
		c := colors[i%len(colors)]
		b.WriteString(Style(c, line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(DimText("Setup & runtime universal untuk proyek Laravel"))
	b.WriteString("\n")
	b.WriteString(DimText("By MaleoLabs"))
	return strings.TrimRight(b.String(), "\n")
}