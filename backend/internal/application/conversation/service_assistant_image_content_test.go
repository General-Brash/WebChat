package conversation

import (
	"strings"
	"testing"
)

const assistantImageTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p94AAAAASUVORK5CYII="

func TestExtractAssistantImageCandidatesFromRawBase64(t *testing.T) {
	candidates := extractAssistantImageCandidates("\n" + assistantImageTestPNG + "\n")
	if len(candidates) != 1 {
		t.Fatalf("expected one image candidate, got %d", len(candidates))
	}
	_, mimeType, ok := decodeAssistantImageCandidate(candidates[0])
	if !ok {
		t.Fatal("expected raw base64 candidate to decode")
	}
	if mimeType != "image/png" {
		t.Fatalf("expected image/png, got %q", mimeType)
	}
}

func TestExtractAssistantImageCandidatesFromJSONPayload(t *testing.T) {
	payload := `{"data":[{"b64_json":"` + assistantImageTestPNG + `"}]}`
	candidates := extractAssistantImageCandidates(payload)
	if len(candidates) != 1 {
		t.Fatalf("expected one image candidate, got %d", len(candidates))
	}
	_, mimeType, ok := decodeAssistantImageCandidate(candidates[0])
	if !ok {
		t.Fatal("expected JSON b64_json candidate to decode")
	}
	if mimeType != "image/png" {
		t.Fatalf("expected image/png, got %q", mimeType)
	}
}

func TestExtractAssistantImageCandidatesFromMarkdownDataImage(t *testing.T) {
	payload := "![Generated image](data:image/png;base64," + assistantImageTestPNG + ")"
	candidates := extractAssistantImageCandidates(payload)
	if len(candidates) != 1 {
		t.Fatalf("expected one image candidate, got %d", len(candidates))
	}
	if candidates[0].MIMEType != "image/png" {
		t.Fatalf("expected declared MIME from data URL, got %q", candidates[0].MIMEType)
	}
	_, mimeType, ok := decodeAssistantImageCandidate(candidates[0])
	if !ok {
		t.Fatal("expected Markdown data image candidate to decode")
	}
	if mimeType != "image/png" {
		t.Fatalf("expected image/png, got %q", mimeType)
	}
}

func TestExtractAssistantImageCandidatesIgnoresMixedText(t *testing.T) {
	payload := "Here is the image payload: " + assistantImageTestPNG
	if candidates := extractAssistantImageCandidates(payload); len(candidates) != 0 {
		t.Fatalf("expected mixed prose to be ignored, got %d candidate(s)", len(candidates))
	}
}

func TestExtractAssistantImageCandidatesFromFencedJSONPayload(t *testing.T) {
	payload := strings.Join([]string{
		"```json",
		`{"image_base64":"` + assistantImageTestPNG + `"}`,
		"```",
	}, "\n")
	candidates := extractAssistantImageCandidates(payload)
	if len(candidates) != 1 {
		t.Fatalf("expected one image candidate, got %d", len(candidates))
	}
	_, _, ok := decodeAssistantImageCandidate(candidates[0])
	if !ok {
		t.Fatal("expected fenced JSON candidate to decode")
	}
}
