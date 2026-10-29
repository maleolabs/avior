package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestErrorCardFramesEveryLine(t *testing.T) {
	var buf bytes.Buffer
	ErrorCard(&buf, "Baris A\nBaris B")
	got := buf.String()
	for _, want := range []string{"╭─ Error", "│ Baris A\n", "│ Baris B\n", "│ Periksa detail", "╰─"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ErrorCard output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderErrorPlainErrorUsesCard(t *testing.T) {
	var buf bytes.Buffer
	RenderError(&buf, errors.New("validasi .env: APP_KEY wajib"))
	if !strings.Contains(buf.String(), "│ validasi .env: APP_KEY wajib\n") {
		t.Fatalf("plain error harus dirender sebagai kartu:\n%s", buf.String())
	}
}

func TestRenderErrorReportedUsesOwnRender(t *testing.T) {
	var buf bytes.Buffer
	err := NewReportedError(errors.New("exit status 1: detail mentah"), "Langkah gagal: migrasi")
	if err.UserMessage() != "Langkah gagal: migrasi" {
		t.Fatalf("UserMessage = %q", err.UserMessage())
	}
	RenderError(&buf, err)
	got := buf.String()
	if !strings.Contains(got, "│ Langkah gagal: migrasi\n") {
		t.Fatalf("kartu harus pakai user message:\n%s", got)
	}
	if strings.Contains(got, "detail mentah") {
		t.Fatalf("detail internal tidak boleh bocor ke render:\n%s", got)
	}
	// errors.As tetap menemukan wrapper dan Unwrap membawa error asli.
	var re ReportedError
	if !errors.As(err, &re) {
		t.Fatal("ReportedError harus terdeteksi errors.As")
	}
	if re.UserMessage() != "Langkah gagal: migrasi" {
		t.Fatalf("UserMessage lewat interface = %q", re.UserMessage())
	}
}

func TestRenderErrorWrappedReportedDetected(t *testing.T) {
	var buf bytes.Buffer
	wrapped := errors.Join(errors.New("other"), NewReportedError(errors.New("x"), "pesan aman"))
	RenderError(&buf, wrapped)
	if !strings.Contains(buf.String(), "│ pesan aman\n") {
		t.Fatalf("ReportedError ter-wrapped harus dirender:\n%s", buf.String())
	}
}
