// Banner merender logo unicode (block elements) untuk root command.
package ui

import (
	"strings"
)

// bannerLines adalah logo dalam Unicode block elements (box-drawing).
var bannerLines = []string{
	"█████╗ ██╗   ██╗██╗ ██████╗ ██████╗ ",
	"██╔══██╗██║   ██║██║██╔═══██╗██╔══██╗",
	"███████║██║   ██║██║██║   ██║██████╔╝",
	"██╔══██║╚██╗ ██╔╝██║██║   ██║██╔══██╗",
	"██║  ██║ ╚████╔╝ ██║╚██████╔╝██║  ██║",
	"╚═╝  ╚═╝  ╚═══╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝",
}

// Banner mengembalikan logo (unicode) + tagline + "By MaleoLabs".
// Warna otomatis non-aktif di non-TTY (NO_COLOR/pipe).
func Banner() string {
	var b strings.Builder
	for _, line := range bannerLines {
		b.WriteString(Style(Primary, line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(DimText("Setup & runtime universal untuk proyek Laravel"))
	b.WriteString("\n")
	b.WriteString(DimText("By MaleoLabs"))
	return strings.TrimRight(b.String(), "\n")
}