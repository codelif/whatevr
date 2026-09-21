package wa

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	appstore "whatevrd/internal/store"
)

// testThumbnailJPEG encodes a picture of a known shape, so a test can tell
// whether the stored dimensions were measured or believed.
func testThumbnailJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			canvas.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, canvas, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode thumbnail: %v", err)
	}
	return out.Bytes()
}

// A link preview rides an ordinary text row. The row must stay a text row: its
// text is still the message, and everything that reads a message by its kind
// (the chat list, a reply quote, search) must go on seeing exactly that.
func TestLinkPreviewRidesTheTextItBelongsTo(t *testing.T) {
	client := newMediaIngestClient(t)
	thumbnail := testThumbnailJPEG(t, 200, 112)

	input, ok := client.textMessageInput(context.Background(), mediaIngestEvent("lp1", &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:          proto.String("look at this https://www.Example.com/road/to/nowhere?x=1"),
			MatchedText:   proto.String("https://www.Example.com/road/to/nowhere?x=1"),
			Title:         proto.String("The road to nowhere"),
			Description:   proto.String("A page about a road."),
			PreviewType:   waE2E.ExtendedTextMessage_IMAGE.Enum(),
			JPEGThumbnail: thumbnail,
		},
	}), ingestOptions{source: sourceLive})
	if !ok {
		t.Fatal("a message with a link preview must still ingest as a message")
	}
	if input.Text != "look at this https://www.Example.com/road/to/nowhere?x=1" {
		t.Fatalf("text = %q, the preview must not have replaced the message", input.Text)
	}

	preview := appstore.DecodePayload(input.PayloadJSON).LinkPreview
	if preview == nil {
		t.Fatal("no link preview was stored")
	}
	if preview.Title != "The road to nowhere" || preview.Description != "A page about a road." {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Type != "image" {
		t.Fatalf("type = %q", preview.Type)
	}
	// The host is what a reader checks a link against, so it is lowercased and
	// stripped of the "www." that says nothing.
	if preview.Host != "example.com" {
		t.Fatalf("host = %q", preview.Host)
	}
	// The URL keeps its query and its case: it is what a click opens, and a
	// path is not a hostname.
	if preview.URL != "https://www.Example.com/road/to/nowhere?x=1" {
		t.Fatalf("url = %q", preview.URL)
	}
	if preview.ThumbnailPath == "" {
		t.Fatal("the inline thumbnail was not written to the cache")
	}
	if _, err := os.Stat(preview.ThumbnailPath); err != nil {
		t.Fatalf("stat thumbnail: %v", err)
	}
	// Measured from the picture, not taken from the message: a card that
	// reserved the wrong shape would resize the moment the image decoded.
	if preview.ThumbnailWidth != 200 || preview.ThumbnailHeight != 112 {
		t.Fatalf("thumbnail = %dx%d", preview.ThumbnailWidth, preview.ThumbnailHeight)
	}
}

// A preview with nothing but the URL it matched is not a card. The text
// already contains that URL and already renders as a link, so drawing a box
// under it that repeats it is worse than drawing nothing.
func TestBareLinkGetsNoCard(t *testing.T) {
	client := newMediaIngestClient(t)

	input, ok := client.textMessageInput(context.Background(), mediaIngestEvent("lp2", &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String("https://example.com/plain"),
			MatchedText: proto.String("https://example.com/plain"),
		},
	}), ingestOptions{source: sourceLive})
	if !ok {
		t.Fatal("the message itself must still ingest")
	}
	if input.PayloadJSON != "" {
		t.Fatalf("payload = %q, a bare link must store nothing", input.PayloadJSON)
	}
}

// A plain message must not pay for the feature: no payload, no thumbnail file,
// nothing on the row that was not there before.
func TestPlainTextStoresNoPayload(t *testing.T) {
	client := newMediaIngestClient(t)

	input, ok := client.textMessageInput(context.Background(), mediaIngestEvent("lp3", &waE2E.Message{
		Conversation: proto.String("no links here"),
	}), ingestOptions{source: sourceLive})
	if !ok {
		t.Fatal("a plain message must ingest")
	}
	if input.PayloadJSON != "" {
		t.Fatalf("payload = %q", input.PayloadJSON)
	}
}

func TestLinkPreviewURLNormalization(t *testing.T) {
	for _, tc := range []struct {
		name      string
		matched   string
		canonical string
		host      string
	}{
		// Senders' clients match the text as typed, and people do not type
		// schemes. A card that could not be opened would be the result.
		{"scheme assumed", "example.com/x", "https://example.com/x", "example.com"},
		{"www stripped", "http://www.bbc.co.uk/news", "http://www.bbc.co.uk/news", "bbc.co.uk"},
		{"port is not part of the host", "https://localhost:8080/x", "https://localhost:8080/x", "localhost"},
		// A preview is a preview of a page. Everything else has no site to
		// name and nothing a browser would do with it.
		{"mailto is not a page", "mailto:someone@example.com", "", ""},
		{"tel is not a page", "tel:+15551234", "", ""},
		{"nothing matched", "", "", ""},
		{"no host", "https:///justapath", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical, host := linkPreviewURL(tc.matched)
			if canonical != tc.canonical || host != tc.host {
				t.Fatalf("got (%q, %q), want (%q, %q)", canonical, host, tc.canonical, tc.host)
			}
		})
	}
}

// Deleting a message for everyone has to take its link preview with it. The
// row keeps its place in the transcript and loses everything else; a card
// still naming the page somebody deleted would be the message surviving its
// own deletion.
func TestRevokeDropsTheCard(t *testing.T) {
	ctx := context.Background()
	client := newMediaIngestClient(t)

	input, ok := client.textMessageInput(ctx, mediaIngestEvent("lp4", &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        proto.String("https://example.com/secret"),
			MatchedText: proto.String("https://example.com/secret"),
			Title:       proto.String("Something private"),
		},
	}), ingestOptions{source: sourceLive})
	if !ok {
		t.Fatal("ingest failed")
	}
	saved, err := client.store.SaveTextMessage(ctx, input)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if appstore.DecodePayload(saved.Message.PayloadJSON).LinkPreview == nil {
		t.Fatal("the preview was not stored in the first place")
	}

	revoked, _, _, err := client.store.MarkMessageRevoked(ctx, input.ID, false)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked.PayloadJSON != "" {
		t.Fatalf("payload survived the revoke: %q", revoked.PayloadJSON)
	}
}

// A hero card reserves the shape of the picture it will actually draw.
//
// WhatsApp sends two pictures with a link preview: a ~90px square placeholder
// inline, and a full-size one it offers to fetch, whose dimensions the message
// states. Measuring the inline one made every hero square, so a 16:9 still was
// cropped into a box and blown up five times over. The phone shows it wide,
// because the phone believes the stated shape.
func TestLinkPreviewReservesTheFullSizePictureShape(t *testing.T) {
	client := newMediaIngestClient(t)
	// The placeholder really is square; the real picture is 16:9.
	placeholder := testThumbnailJPEG(t, 90, 90)

	input, ok := client.textMessageInput(context.Background(), mediaIngestEvent("lp-hero", &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:                proto.String("https://www.netflix.com/in/title/82924249"),
			MatchedText:         proto.String("https://www.netflix.com/in/title/82924249"),
			Title:               proto.String("Watch Papa Yaar by Zakir Khan"),
			PreviewType:         waE2E.ExtendedTextMessage_VIDEO.Enum(),
			JPEGThumbnail:       placeholder,
			ThumbnailWidth:      proto.Uint32(640),
			ThumbnailHeight:     proto.Uint32(360),
			ThumbnailDirectPath: proto.String("/v/t62.000/whatever"),
			MediaKey:            []byte("0123456789abcdef0123456789abcdef"),
		},
	}), ingestOptions{source: sourceLive})
	if !ok {
		t.Fatal("a message with a link preview must still ingest as a message")
	}

	preview := appstore.DecodePayload(input.PayloadJSON).LinkPreview
	if preview == nil {
		t.Fatal("no link preview was stored")
	}
	if preview.ThumbnailWidth != 640 || preview.ThumbnailHeight != 360 {
		t.Fatalf("the card reserved %dx%d, the placeholder's shape, not the picture's 640x360",
			preview.ThumbnailWidth, preview.ThumbnailHeight)
	}
	if preview.ThumbnailPath == "" {
		t.Fatal("the inline placeholder was not kept, so the card has nothing to draw until the fetch lands")
	}
}

// With nothing stated, the inline picture is all there is, so its own shape is
// the honest answer.
func TestLinkPreviewFallsBackToMeasuringTheInlinePicture(t *testing.T) {
	client := newMediaIngestClient(t)

	input, ok := client.textMessageInput(context.Background(), mediaIngestEvent("lp-plain", &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:          proto.String("https://example.com/page"),
			MatchedText:   proto.String("https://example.com/page"),
			Title:         proto.String("A page"),
			PreviewType:   waE2E.ExtendedTextMessage_IMAGE.Enum(),
			JPEGThumbnail: testThumbnailJPEG(t, 200, 112),
		},
	}), ingestOptions{source: sourceLive})
	if !ok {
		t.Fatal("a message with a link preview must still ingest as a message")
	}

	preview := appstore.DecodePayload(input.PayloadJSON).LinkPreview
	if preview == nil {
		t.Fatal("no link preview was stored")
	}
	if preview.ThumbnailWidth != 200 || preview.ThumbnailHeight != 112 {
		t.Fatalf("measured %dx%d, want the inline picture's 200x112",
			preview.ThumbnailWidth, preview.ThumbnailHeight)
	}
}
